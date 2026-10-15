#!/usr/bin/python3
"""Persistent per-run systemd owner and exact-container bounded log collector.

Installed root owned. Never launched by the UI, no shell command/manifest path
arguments. systemd instance carries the immutable root-created run ID.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import sys
import time

sys.path.insert(0, '/opt/spark/model-collection-v4/bin')
from modelctl import (BASE_ENV, IDS, LOG_ROOT, Command, Controller, ObservationError,
                      Refusal, RotatingSink, StateStore, SystemAdapter,
                      load_production_manifest, proc_birth, protect, unit_name, legacy_support)


def container_argv(row, run):
    if row['id'] in IDS[5:]:
        return legacy_support().container_argv(row, run)
    runtime = row['runtime']
    args = ['/usr/bin/docker', 'create', '--name', 'spark-v4-' + run['run_id'],
            '--label', 'spark.v4.model=' + row['id'],
            '--label', 'spark.v4.run=' + run['run_id'],
            '--label', 'spark.v4.generation=' + str(run['generation']),
            '--label', 'spark.v4.attempt=' + run['attempt'],
            '--user', '1000:1000', '--cap-drop', 'ALL',
            '--security-opt', 'no-new-privileges=true', '--read-only',
            '--network', 'host', '--gpus', 'all', '--restart', 'no',
            '--pids-limit', '4096', '--shm-size', str(runtime.get('shm_bytes', 2 * 1024**3)),
            '--tmpfs', '/tmp:rw,nosuid,nodev,size=' + str(runtime.get('tmpfs_bytes', 2 * 1024**3)),
            '--log-driver', 'local', '--log-opt', 'max-size=10m',
            '--log-opt', 'max-file=3']
    for mount in runtime['mounts']:
        args += ['--mount', 'type=bind,source=' + mount['source'] + ',target=' + mount['target'] + ',readonly']
    for key, value in sorted(runtime.get('env', {}).items()):
        args += ['--env', key + '=' + value]
    # ENTRYPOINT override makes the exact audited engine command authoritative.
    args += ['--entrypoint', runtime['argv'][0], runtime['image'], *runtime['argv'][1:]]
    return args


class Supervisor:
    def __init__(self, ctl, mid, run_id, invocation_id, command=None):
        self.ctl, self.mid, self.run_id = ctl, mid, run_id
        self.invocation = invocation_id
        self.command = command or Command()
        self.signal_requested = False
        self.engine_log = RotatingSink(ctl.log_root / mid / 'engine.log', protected=ctl.protected)

    def current(self, data):
        run = data['runs'].get(self.mid)
        if not run or run['run_id'] != self.run_id:
            raise Refusal('Old/replaced worker cannot adopt another generation')
        return run

    def approved_launch(self, row, run):
        if self.mid == IDS[5] and run.get('legacy_handoff_invocation') not in {None, self.invocation}:
            raise Refusal('Qwen preparation invocation replaced before engine creation')
        frozen = run.get('approved_profile_revision')
        if not frozen or frozen != self.ctl.profile_revision(self.mid):
            raise Refusal('Accepted launch definition changed or lacks frozen approval')
        operation_id = run.get('bridge_operation_id')
        if operation_id:
            operation = self.ctl.store.read().get('bridge_operations', {}).get(operation_id)
            if not operation or operation.get('execution_forbidden'):
                raise Refusal('Unexecuted operation authority revoked before engine launch')
        validate_definition = getattr(self.ctl.adapter, 'validate_run_definition', None)
        if validate_definition is not None:
            validate_definition(row, run)

    def launch(self):
        row = self.ctl.row(self.mid)
        with self.ctl.store.lock():
            data = self.ctl.store.read(); run = self.current(data)
            if run.get('cancelled') or run['state'] != 'STARTING' or data['reservation'] != self.mid or not row['prepared']:
                raise Refusal('Cancelled/incomplete worker: no container created')
            self.approved_launch(row, run)
            unit = self.ctl.adapter.unit(self.mid, self.run_id)
            pid = os.getpid()
            if unit.get('InvocationID') != self.invocation or int(unit.get('MainPID') or 0) != pid or not proc_birth(pid):
                raise Refusal('Supervisor service invocation/PID does not match self')
            run['owner'] = {'unit': unit_name(self.mid, self.run_id), 'invocation_id': self.invocation,
                            'pid': pid, 'birth': proc_birth(pid)}
            self.ctl.adapter.recheck_bundle([(Path(p), tuple(w)) for p, w in run.get('artifact_witnesses', [])])
            self.ctl.adapter.preflight(row)  # Recheck foreign resources at durable worker handoff.
            self.ctl.store.write(data)
            # Creation is an independently journaled supervisor stage. Release the
            # collection lock while Docker creates the inert container (up to 10s).
            # A concurrent stop can record cancellation immediately; the second
            # lock below prevents the created container from ever starting afterward.
            run['container_create_attempted'] = True
            self.ctl.store.write(data)
        self.engine_log.write('\n' + time.strftime('%Y-%m-%dT%H:%M:%S%z') + ' RUN ' + self.run_id + ' generation=' + str(run['generation']) + '\n')
        cid = self.command(container_argv(row, run), 10.0).strip()
        if len(cid) != 64 or any(c not in '0123456789abcdef' for c in cid):
            raise ObservationError('Docker create did not return exact full ID')
        with self.ctl.store.lock():
            data = self.ctl.store.read(); run = self.current(data)
            run['container_id'] = cid
            try:
                observed = self.ctl.adapter.inspect_container(cid)
                created = observed.get('Created')
                if not isinstance(created, str) or not 1 <= len(created) <= 128:
                    raise ObservationError('Docker create lacks exact creation identity')
            except Exception:
                # Preserve an unresolved create outcome, never infer release.
                self.ctl.store.write(data)
                raise
            # Publish the exact CID/Created pair together. Read-only observers do
            # not take this lock; a CID-only intermediate snapshot is ambiguous.
            run['container_created'] = created
            self.ctl.store.write(data)
            if row['id'] in IDS[5:]:
                legacy = legacy_support()
                run['legacy_declaration_sha256'] = legacy.check_declaration(row, observed, legacy.read_secret(), self.ctl.adapter)
            self.ctl.store.write(data)
            self.ctl.adapter.observe(row, run)  # exact image/labels; no model load yet
        # A stop can enter between create and start; recheck cancellation under lock.
        with self.ctl.store.lock():
            data = self.ctl.store.read(); run = self.current(data)
            if run.get('cancelled') or self.signal_requested:
                return run
            if run['state'] != 'STARTING' or data['reservation'] != self.mid:
                raise Refusal('Worker no longer owns the exact collection reservation; no engine start')
            self.approved_launch(row, run)
            self.ctl.adapter.recheck_bundle([(Path(p), tuple(w)) for p, w in run.get('artifact_witnesses', [])])
            self.command(['/usr/bin/docker', 'start', run['container_id']], 3.0)
            observed = self.ctl.adapter.inspect_container(run['container_id'])
            pid = int(observed.get('State', {}).get('Pid') or 0)
            if observed.get('State', {}).get('Running'):
                run['engine_pid'], run['engine_birth'] = pid, proc_birth(pid)
            self.ctl.store.write(data)
        return run

    def cancel(self):
        with self.ctl.store.lock():
            data = self.ctl.store.read(); run = self.current(data)
            run.update(cancelled=True, state='STOPPING', message='Supervisor received shutdown/cancellation')
            self.ctl.store.write(data)
        # Never call systemctl stop on own service from its own shutdown handler.
        observed = self.ctl.adapter.observe(self.ctl.row(self.mid), run)
        if observed['engine_active']:
            self.command(['/usr/bin/docker', 'stop', '--time', '90' if self.mid in IDS[5:] else '20', run['container_id']], 95 if self.mid in IDS[5:] else 25)
        return run

    def follow(self, run):
        if not run.get('container_id'):
            return
        # Docker owns bounded durable originals; this systemd owner mirrors streams
        # from first startup, independent of the action waiter/TUI/SSH session.
        collector = subprocess.Popen(['/usr/bin/docker', 'logs', '--follow', '--timestamps', run['container_id']],
                                     stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                     stderr=subprocess.STDOUT, env=BASE_ENV, cwd='/', close_fds=True)
        sel = selectors.DefaultSelector()
        assert collector.stdout is not None
        os.set_blocking(collector.stdout.fileno(), False)
        sel.register(collector.stdout, selectors.EVENT_READ)
        stop_sent = False
        try:
            while True:
                current = self.current(self.ctl.store.read())
                if (self.signal_requested or current.get('cancelled')) and not stop_sent:
                    self.cancel(); stop_sent = True
                for key, _ in sel.select(.1):
                    block = os.read(key.fd, 65536)
                    if block:
                        self.engine_log.write(block)
                    else:
                        sel.unregister(key.fd)
                if collector.poll() is not None and not sel.get_map():
                    if collector.returncode:
                        raise ObservationError('Exact-container log collector failed')
                    return
        finally:
            sel.close(); collector.stdout.close()
            if collector.poll() is None:
                # Only disposable owned log helper; not an engine signal.
                collector.terminate()
                try:
                    collector.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    collector.kill(); collector.wait(timeout=2)
            else:
                collector.wait()

    def run(self):
        try:
            run = self.launch()
            if self.signal_requested or run.get('cancelled'):
                run = self.cancel()
            self.follow(run)
            with self.ctl.store.lock():
                data = self.ctl.store.read(); latest = self.current(data)
                if latest.get('cancelled'):
                    latest['message'] = 'Engine stopped; supervisor exiting; resource release checked by controller'
                else:
                    latest.update(state='FAILED', message='Engine exited/crashed; no automatic restart')
                self.ctl.store.write(data)
            self.ctl.log(self.mid, 'SUPERVISOR EXIT run=' + self.run_id)
            return 0
        except Exception as exc:
            try:
                self.ctl._fail(self.mid, self.run_id, 'Persistent supervisor failure: ' + str(exc))
                self.engine_log.write(time.strftime('%Y-%m-%dT%H:%M:%S%z') + ' SUPERVISOR FAILURE: ' + str(exc)[:400] + '\n')
            except Exception:
                pass
            # Do not erase ownership or silently kill a live exact container on
            # collector/permission error. FAILED+active blocks another model.
            return 1


def main():
    if os.geteuid() != 0 or len(sys.argv) != 3 or sys.argv[1] not in IDS:
        print('Supervisor requires installed root systemd unit with fixed model/run ID', file=sys.stderr)
        return 1
    protect(Path(__file__).resolve())
    manifest = load_production_manifest()
    # unit_name performs strict run UUID validation.
    unit_name(sys.argv[1], sys.argv[2])
    invocation = os.environ.get('INVOCATION_ID', '')
    if len(invocation) != 32 or any(c not in '0123456789abcdef' for c in invocation):
        raise Refusal('Native systemd invocation ID missing')
    ctl = Controller(manifest, SystemAdapter(manifest))
    worker = Supervisor(ctl, sys.argv[1], sys.argv[2], invocation)
    def requested(signum, frame):
        worker.signal_requested = True
    signal.signal(signal.SIGTERM, requested)
    signal.signal(signal.SIGINT, requested)
    return worker.run()


if __name__ == '__main__':
    raise SystemExit(main())
