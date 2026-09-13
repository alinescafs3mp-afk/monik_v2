package servicehost

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func Dial(stateDir string) (net.Conn, error) {
	path := DefaultSock(stateDir)
	if runtime.GOOS == "windows" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return net.DialTimeout("tcp", string(b), 3*time.Second)
	}
	return net.DialTimeout("unix", path, 3*time.Second)
}

func Call(stateDir string, req Request) (Response, error) {
	if runtime.GOOS == "windows" {
		return fileCall(stateDir, req)
	}
	c, err := Dial(stateDir)
	if err != nil {
		return Response{}, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		return resp, fmt.Errorf("%s", resp.Message)
	}
	return resp, nil
}

func fileCall(stateDir string, req Request) (Response, error) {
	reqPath := filepath.Join(stateDir, "control-request.json")
	respPath := filepath.Join(stateDir, "control-response.json")
	_ = os.Remove(respPath)
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	tmp := reqPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return Response{}, err
	}
	if err := os.Rename(tmp, reqPath); err != nil {
		return Response{}, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(respPath)
		if err == nil && len(raw) > 0 {
			var resp Response
			if err := json.Unmarshal(raw, &resp); err != nil {
				return Response{}, err
			}
			_ = os.Remove(respPath)
			if !resp.OK {
				return resp, fmt.Errorf("%s", resp.Message)
			}
			return resp, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return Response{}, fmt.Errorf("service host did not answer the control request")
}
