package spark

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type dockerObject struct {
	ID         string          `json:"Id"`
	Image      string          `json:"Image"`
	Name       string          `json:"Name"`
	Config     json.RawMessage `json:"Config"`
	HostConfig json.RawMessage `json:"HostConfig"`
	State      struct {
		Status     string    `json:"Status"`
		Running    bool      `json:"Running"`
		Restarting bool      `json:"Restarting"`
		PID        int       `json:"Pid"`
		StartedAt  time.Time `json:"StartedAt"`
		FinishedAt time.Time `json:"FinishedAt"`
	} `json:"State"`
}

// Only these fixed paths/methods are used. This client is NOT exposed to the
// collector; its requests are constructed after exact local profile approval.
func dockerClient(p Profile) *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", p.Target.DockerSocket)
	}, DisableCompression: true, MaxResponseHeaderBytes: 8192}, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func dockerRequest(ctx context.Context, p Profile, method, path string, body any) ([]byte, int, error) {
	var data []byte
	var e error
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return nil, 0, e
		}
	}
	client := dockerClient(p)
	defer client.CloseIdleConnections()
	req, e := http.NewRequestWithContext(ctx, method, "http://docker"+path, bytes.NewReader(data))
	if e != nil {
		return nil, 0, e
	}
	req.Header.Set("Content-Type", "application/json")
	r, e := client.Do(req)
	if e != nil {
		return nil, 0, fmt.Errorf("Docker supervisor unavailable")
	}
	defer r.Body.Close()
	b, e := io.ReadAll(io.LimitReader(r.Body, 256<<10+1))
	if e != nil || len(b) > 256<<10 {
		return nil, r.StatusCode, fmt.Errorf("Docker response budget exceeded")
	}
	return b, r.StatusCode, nil
}
func dockerID(p Profile) string {
	if p.Target.ContainerID != "" {
		return p.Target.ContainerID
	}
	return p.Target.Frozen.Name
}
func dockerObjectRead(ctx context.Context, p Profile) (dockerObject, int, error) {
	b, status, e := dockerRequest(ctx, p, "GET", "/containers/"+url.PathEscape(dockerID(p))+"/json", nil)
	var o dockerObject
	if e != nil {
		return o, status, e
	}
	if status == 404 {
		return o, status, nil
	}
	if status != 200 {
		return o, status, fmt.Errorf("Docker inspect failed")
	}
	if e = json.Unmarshal(b, &o); e != nil {
		return o, status, fmt.Errorf("invalid Docker response")
	}
	if len(o.ID) != 64 || !isHex(o.ID) {
		return o, status, fmt.Errorf("Docker identity unavailable")
	}
	if p.Target.Frozen == nil && o.ID != p.Target.ContainerID {
		return o, status, fmt.Errorf("container identity drift")
	}
	return o, status, nil
}
func dockerDefinition(ctx context.Context, p Profile) (string, error) {
	if p.Target.Frozen != nil {
		if e := p.Target.Frozen.validate(); e != nil {
			return "", e
		}
		// Recreated containers are accepted only when their inspected effective
		// definition matches the frozen declaration, not a recycled friendly name.
		o, status, e := dockerObjectRead(ctx, p)
		if e != nil {
			return "", e
		}
		if status != 404 {
			if e = dockerMatchesFrozen(o, *p.Target.Frozen); e != nil {
				return "", e
			}
		}
		return digest(p.Target.Frozen), nil
	}
	o, status, e := dockerObjectRead(ctx, p)
	if e != nil || status == 404 {
		return "", fmt.Errorf("approved container absent")
	}
	return digest([]any{o.Image, o.Config, o.HostConfig}), nil
}
func dockerMatchesFrozen(o dockerObject, d DockerDeclaration) error {
	if e := d.validate(); e != nil {
		return e
	}
	var config struct {
		Image       string
		User        string
		Cmd         []string
		Env         []string
		WorkingDir  string
		Entrypoint  []string
		StopTimeout int
	}
	var hc struct {
		Privileged     bool
		ReadonlyRootfs bool
		AutoRemove     bool
		NetworkMode    string
		Binds          []string
		Mounts         []struct {
			Type, Source, Target string
			ReadOnly             bool
		}
		CapAdd         []string
		CapDrop        []string
		SecurityOpt    []string
		DeviceRequests []DockerDeviceRequest
		Devices        []json.RawMessage
		PidMode        string
		IpcMode        string
		ShmSize        int64
		RestartPolicy  struct{ Name string }
	}
	if json.Unmarshal(o.Config, &config) != nil || json.Unmarshal(o.HostConfig, &hc) != nil {
		return fmt.Errorf("unverifiable frozen container")
	}
	// The engine expands image defaults. Approve the resolved Config/HostConfig
	// fingerprint with inspect for existing objects; recreation uses an explicit
	// empty Entrypoint and all fixed effective values below.
	if config.Image != d.Image || config.User != d.User || digest(config.Cmd) != digest(d.Command) || digest(config.Env) != digest(d.Environment) || config.WorkingDir != d.WorkingDir || len(config.Entrypoint) > 0 || config.StopTimeout != d.StopTimeout || hc.Privileged || hc.NetworkMode != d.NetworkMode || hc.ReadonlyRootfs != d.ReadonlyRootfs || hc.AutoRemove != d.AutoRemove || len(hc.Binds) > 0 || len(hc.CapAdd) > 0 || len(hc.Devices) > 0 || hc.PidMode != "" || hc.IpcMode != d.IPCMode || hc.ShmSize != d.ShmSize || digest(hc.DeviceRequests) != digest(d.DeviceRequests) || len(hc.Mounts) != len(d.Mounts) || len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" || len(hc.SecurityOpt) != 1 || !member(hc.SecurityOpt[0], "no-new-privileges", "no-new-privileges:true") || hc.RestartPolicy.Name != "no" {
		return fmt.Errorf("frozen container definition drift")
	}
	for i, m := range hc.Mounts {
		want := d.Mounts[i]
		if m.Type != want.Type || m.Source != want.Source || m.Target != want.Target || m.ReadOnly != want.ReadOnly {
			return fmt.Errorf("frozen mount drift")
		}
	}
	return nil
}
func dockerInspect(ctx context.Context, p Profile) (Instance, error) {
	o, status, e := dockerObjectRead(ctx, p)
	if e != nil {
		return Instance{}, e
	}
	if status == 404 && p.Target.Frozen != nil {
		return Instance{State: "stopped", Identity: "stopped", Available: true}, nil
	}
	if status != 200 {
		return Instance{}, fmt.Errorf("approved container absent")
	}
	if p.Target.Frozen != nil {
		if e = dockerMatchesFrozen(o, *p.Target.Frozen); e != nil {
			return Instance{}, e
		}
	}
	state := "stopped"
	if o.State.Running {
		state = "running"
	}
	if o.State.Restarting {
		state = "starting"
	}
	if o.State.Status == "dead" {
		state = "failed"
	}
	id := "stopped"
	if state == "running" || state == "starting" {
		id = digest([]any{o.ID, o.State.StartedAt})
	}
	return Instance{State: state, Identity: id, PID: o.State.PID, StartedAt: o.State.StartedAt, Available: true}, nil
}
func dockerEffect(ctx context.Context, p Profile, action string) (Instance, error) {
	o, status, e := dockerObjectRead(ctx, p)
	if e != nil {
		return Instance{}, e
	}
	if status == 200 {
		if e = validateDockerBinding(p, o); e != nil {
			return Instance{}, e
		}
	}
	if action == "start" && status == 404 && p.Target.Frozen != nil {
		d := p.Target.Frozen
		if e = d.validate(); e != nil {
			return Instance{}, e
		}
		mounts := []map[string]any{}
		for _, m := range d.Mounts {
			mounts = append(mounts, map[string]any{"Type": m.Type, "Source": m.Source, "Target": m.Target, "ReadOnly": m.ReadOnly})
		}
		body := map[string]any{"Image": d.Image, "User": d.User, "Cmd": d.Command, "Entrypoint": []string{}, "Env": d.Environment, "WorkingDir": d.WorkingDir, "StopTimeout": d.StopTimeout, "HostConfig": map[string]any{"Mounts": mounts, "NetworkMode": d.NetworkMode, "Privileged": false, "ReadonlyRootfs": d.ReadonlyRootfs, "AutoRemove": d.AutoRemove, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges"}, "RestartPolicy": map[string]string{"Name": "no"}, "DeviceRequests": d.DeviceRequests, "IpcMode": d.IPCMode, "ShmSize": d.ShmSize}}
		b, sc, err := dockerRequest(ctx, p, "POST", "/containers/create?name="+url.QueryEscape(d.Name), body)
		if err != nil || sc != 201 {
			return Instance{}, fmt.Errorf("frozen container creation failed; no pull attempted")
		}
		var created struct {
			ID string `json:"Id"`
		}
		if json.Unmarshal(b, &created) != nil || len(created.ID) != 64 || !isHex(created.ID) {
			return Instance{}, fmt.Errorf("container creation identity missing")
		}
		// The create response alone does not prove the effective definition.
		// Inspect the returned immutable ID, never a concurrently recycled name,
		// and check all frozen safety properties before the start POST.
		createdProfile := p
		createdProfile.Target.ContainerID = created.ID
		createdProfile.Target.Frozen = nil
		o, status, e = dockerObjectRead(ctx, createdProfile)
		if e != nil || status != 200 {
			return Instance{}, fmt.Errorf("created container identity unconfirmed")
		}
		if e = dockerMatchesFrozen(o, *d); e != nil {
			return Instance{}, e
		}
	} else if status != 200 {
		if action == "stop" && status == 404 {
			return Instance{State: "stopped", Identity: "stopped", Available: true}, nil
		}
		return Instance{}, fmt.Errorf("approved container absent")
	}
	path := "/containers/" + url.PathEscape(o.ID) + "/" + action
	if action == "stop" {
		seconds := p.StopSeconds
		if seconds == 0 {
			seconds = 60
		}
		path += fmt.Sprintf("?t=%d", seconds)
	}
	_, sc, e := dockerRequest(ctx, p, "POST", path, nil)
	if e != nil || !member(fmt.Sprint(sc), "204", "304") {
		return Instance{}, fmt.Errorf("Docker lifecycle effect unconfirmed")
	}
	return dockerInspect(ctx, p)
}

type dockerLogReader struct {
	body      io.ReadCloser
	tty       bool
	remaining int64
}

func (d *dockerLogReader) Close() error { return d.body.Close() }
func (d *dockerLogReader) Read(p []byte) (int, error) {
	if d.tty {
		return d.body.Read(p)
	}
	if d.remaining == 0 {
		header := make([]byte, 8)
		if _, e := io.ReadFull(d.body, header); e != nil {
			return 0, e
		}
		if (header[0] != 1 && header[0] != 2) || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return 0, fmt.Errorf("invalid Docker log frame")
		}
		d.remaining = int64(binary.BigEndian.Uint32(header[4:]))
		if d.remaining > 256<<10 {
			return 0, fmt.Errorf("Docker log frame budget exceeded")
		}
	}
	if int64(len(p)) > d.remaining {
		p = p[:d.remaining]
	}
	n, e := d.body.Read(p)
	d.remaining -= int64(n)
	return n, e
}
func dockerLogs(ctx context.Context, p Profile) (LogReader, error) {
	o, status, e := dockerObjectRead(ctx, p)
	if e != nil || status != 200 {
		return nil, fmt.Errorf("log target unavailable")
	}
	if e = validateDockerBinding(p, o); e != nil {
		return nil, e
	}
	client := dockerClient(p)
	client.Timeout = 0
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/"+url.PathEscape(o.ID)+"/logs?follow=1&stdout=1&stderr=1&tail=200&timestamps=0", nil)
	r, e := client.Do(req)
	if e != nil || r.StatusCode != 200 {
		if r != nil {
			r.Body.Close()
		}
		client.CloseIdleConnections()
		return nil, fmt.Errorf("approved Docker logs unavailable")
	}
	var cfg struct{ Tty bool }
	_ = json.Unmarshal(o.Config, &cfg)
	return &dockerLogReader{body: &closeClientBody{ReadCloser: r.Body, client: client}, tty: cfg.Tty}, nil
}
func validateDockerBinding(p Profile, o dockerObject) error {
	if p.Target.Frozen != nil {
		return dockerMatchesFrozen(o, *p.Target.Frozen)
	}
	if digest([]any{o.Image, o.Config, o.HostConfig}) != p.Target.DefinitionHash {
		return fmt.Errorf("approved container definition drift")
	}
	return nil
}

type closeClientBody struct {
	io.ReadCloser
	client *http.Client
}

func (c *closeClientBody) Close() error {
	e := c.ReadCloser.Close()
	c.client.CloseIdleConnections()
	return e
}

var _ = strings.Clone
