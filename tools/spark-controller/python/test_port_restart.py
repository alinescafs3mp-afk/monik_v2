"""Real Linux TCP preflight controls; only owned, ephemeral sockets are used.

Host command observations are injected. The production socket check and local
kernel bind/listen/TIME_WAIT behavior execute without model or service effects.
"""
import errno
import importlib.util
from pathlib import Path
import socket
import struct
import sys
import unittest

if sys.platform.startswith('linux'):
    import fcntl

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('port_restart_candidate', Path(__file__).with_name('modelctl.py'))
core = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = core
spec.loader.exec_module(core)


class LocalSocketAdapter(core.SystemAdapter):
    def __init__(self):
        super().__init__({'excluded_units': []}, command=self.command_without_effects)

    @staticmethod
    def command_without_effects(argv, timeout):
        if argv == ['/usr/bin/nvidia-smi', '--query-gpu=memory.free', '--format=csv,noheader,nounits']:
            return 'N/A'
        raise AssertionError('Unexpected host command: ' + repr(argv))

    def foreign_gpu_containers(self):
        return []

    def gpu_pids(self):
        return []


@unittest.skipUnless(sys.platform.startswith('linux'), 'Linux TCP reuse semantics')
class PortRestartTests(unittest.TestCase):
    def setUp(self):
        self.adapter = LocalSocketAdapter()

    def preflight(self, port):
        self.adapter.preflight({'id': core.IDS[0], 'endpoint': f'http://127.0.0.1:{port}/v1'})

    def listener(self, host='127.0.0.1', reuse=True):
        listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.addCleanup(listener.close)
        listener.settimeout(1)
        if reuse:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        listener.bind((host, 0))
        listener.listen(1)
        return listener

    def assert_occupied(self, port):
        with self.assertRaisesRegex(core.Refusal, 'Selected API port is occupied: ' + str(port)) as result:
            self.preflight(port)
        self.assertEqual(errno.EADDRINUSE, result.exception.__cause__.errno)

    def test_server_time_wait_reuses_port_without_waiting(self):
        listener = self.listener()
        port = listener.getsockname()[1]
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as client:
            client.settimeout(1)
            client.connect(('127.0.0.1', port))
            client_port = client.getsockname()[1]
            server, _ = listener.accept()
            with server:
                server.settimeout(1)
                # The server sends the first FIN; EOF synchronizes each half
                # close, leaving its exact local tuple in TIME_WAIT.
                server.shutdown(socket.SHUT_WR)
                self.assertEqual(b'', client.recv(1))
                client.shutdown(socket.SHUT_WR)
                self.assertEqual(b'', server.recv(1))
        listener.close()

        local = f'0100007F:{port:04X}'
        remote = f'0100007F:{client_port:04X}'
        states = [line.split()[3] for line in Path('/proc/net/tcp').read_text().splitlines()[1:]
                  if line.split()[1:3] == [local, remote]]
        self.assertIn('06', states, 'the owned server tuple must actually be in TIME_WAIT')
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as old_probe:
            with self.assertRaises(OSError) as result:
                old_probe.bind(('127.0.0.1', port))
            self.assertEqual(errno.EADDRINUSE, result.exception.errno)

        self.preflight(port)
        # Pinned llama-server uses SO_REUSEADDR before bind. A real replacement
        # listener can therefore start immediately with the same port.
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as backend:
            backend.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            backend.bind(('127.0.0.1', port))
            backend.listen(1)
            self.assert_occupied(port)

    def test_live_loopback_listener_is_refused_with_or_without_reuse(self):
        for reuse in (False, True):
            with self.subTest(reuse=reuse):
                with self.listener(reuse=reuse) as listener:
                    self.assert_occupied(listener.getsockname()[1])

    def test_live_wildcard_listener_is_refused_with_or_without_reuse(self):
        for reuse in (False, True):
            with self.subTest(reuse=reuse):
                with self.listener(host='0.0.0.0', reuse=reuse) as listener:
                    self.assert_occupied(listener.getsockname()[1])

    def test_distinct_lan_listener_does_not_occupy_loopback(self):
        address = None
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as query:
            for _, name in socket.if_nameindex():
                try:
                    raw = fcntl.ioctl(query.fileno(), 0x8915, struct.pack('256s', name.encode()))
                except OSError:
                    continue  # The interface has no IPv4 address.
                candidate = socket.inet_ntoa(raw[20:24])
                if candidate != '0.0.0.0' and not candidate.startswith('127.'):
                    address = candidate
                    break
        if address is None:
            self.skipTest('No local non-loopback IPv4 interface')
        for reuse in (False, True):
            with self.subTest(reuse=reuse):
                with self.listener(host=address, reuse=reuse) as listener:
                    self.preflight(listener.getsockname()[1])


if __name__ == '__main__':
    unittest.main()
