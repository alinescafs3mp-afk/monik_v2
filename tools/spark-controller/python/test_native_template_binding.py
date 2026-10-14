"""Regression for systemd refusing bare template names in show (no live effects)."""
from pathlib import Path
import os
from unittest.mock import patch
from types import SimpleNamespace
import json
import test_bridge_candidate as base
import test_legacy_shared as old


class NativeTemplateBinding(base.CandidateFixture):
    def test_all_five_configuration_reads_use_inactive_fixed_instance(self):
        core=base.modelctl
        self.ctl.protected=True
        pin=self.root/'fixed-source';pin.write_bytes(b'offline protected-source seam')
        original_open=os.open;seen=[]
        def open_source(path,*args,**kwargs):
            return original_open(pin,*args,**kwargs)
        def show(args,timeout):
            seen.append(args)
            mid=next(mid for mid in core.IDS[:5] if args[2]==core.unit_name(mid,'0'*32))
            return 'FragmentPath=/etc/systemd/system/'+core.unit_name(mid)+'\nDropInPaths=\nNeedDaemonReload=no\n'
        self.adapter.command=show
        with patch.object(core,'protect'),patch.object(core.os,'open',side_effect=open_source):
            for mid in core.IDS[:5]:
                self.assertRegex(self.ctl.profile_revision(mid),r'^[a-f0-9]{64}$')
        self.assertEqual(5,len(seen))
        self.assertTrue(all('@00000000000000000000000000000000.service' in x[2] for x in seen))
        self.assertFalse(self.adapter.handoffs)

    def test_fixed_configuration_probe_does_not_allow_override_or_reload_drift(self):
        core=base.modelctl;self.ctl.protected=True
        for props in ['FragmentPath=/tmp/foreign\nDropInPaths=\nNeedDaemonReload=no\n',
                      'FragmentPath=/etc/systemd/system/'+core.unit_name(self.mid)+'\nDropInPaths=/etc/override.conf\nNeedDaemonReload=no\n',
                      'FragmentPath=/etc/systemd/system/'+core.unit_name(self.mid)+'\nDropInPaths=\nNeedDaemonReload=yes\n']:
            self.adapter.command=lambda args,timeout:props
            with self.assertRaises(core.Refusal):self.ctl.profile_revision(self.mid)


class NativeGPTConfigurationBinding(old.unittest.TestCase):
    def setUp(self):
        self.f=old.LegacyEffectiveUnit();self.f.setUp();self.addCleanup(self.f.doCleanups)
        self.row=old.legacy.definitions()[1]
        path='/etc/systemd/system/'+old.core.unit_name(self.row['id'])
        self.f.files[path]=old.legacy.GPT_UNIT
        self.f.props.update(FragmentPath=path,DropInPaths='',User='root',Group='root',RestartUSec='100ms',ExecStartPre='',ExecStop='',ExecCondition='')
        self.seen=[]
        self.f.adapter.command=lambda args,timeout:self.show(args)
        self.configure('0'*32)
    def configure(self,run):
        cmd='/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/supervisor.py '+self.row['id']+' '+run
        self.f.props['ExecStart']='{ path=/usr/bin/python3 ; argv[]='+cmd+' ; ignore_errors=no ; pid=0 ; }'
    def show(self,args):
        self.seen.append(args)
        self.assertNotIn('@.service',args[2])
        return '\n'.join(k+'='+v for k,v in self.f.props.items())
    def test_configuration_read_binds_expanded_fixed_instance_command(self):
        self.assertTrue(old.legacy.effective_unit(self.f.adapter,self.row)['control_safe'])
        self.assertEqual(old.core.unit_name(self.row['id'],'0'*32),self.seen[0][2])
        self.configure('%i')
        with self.assertRaises(old.core.Refusal):old.legacy.effective_unit(self.f.adapter,self.row)
    def test_real_run_still_uses_its_own_instance_and_expanded_command(self):
        run={'run_id':'a'*32};self.configure(run['run_id'])
        self.assertTrue(old.legacy.effective_unit(self.f.adapter,self.row,run)['control_safe'])
        self.assertEqual(old.core.unit_name(self.row['id'],run['run_id']),self.seen[0][2])
    def test_omitted_cli_empty_arrays_require_exact_typed_dbus_evidence(self):
        commands=[]
        empty=json.dumps({'type':'a(sasbttttuii)','data':[]})
        frames=[empty]*3
        def command(args,timeout):
            commands.append(args)
            if args[0]=='/usr/bin/systemctl':
                return '\n'.join(k+'='+v for k,v in self.f.props.items() if k not in ('ExecStartPre','ExecStop','ExecCondition'))
            self.assertEqual(['/usr/bin/busctl','--system','--json=short','get-property','org.freedesktop.systemd1','/org/freedesktop/systemd1/unit/spark_2dv4_2dgpt_2doss_2d120b_2dexisting_2dconfigured_4000000000000000000000000000000000_2eservice','org.freedesktop.systemd1.Service','ExecStartPre','ExecStop','ExecCondition'],args)
            return '\n'.join(frames)
        self.f.adapter.command=command
        self.assertTrue(old.legacy.effective_unit(self.f.adapter,self.row)['control_safe'])
        self.assertEqual(2,len(commands))
        invalid=[[],[empty]*2,[empty]*4,
                 [json.dumps({'type':'as','data':[]}),empty,empty],
                 [json.dumps({'type':'a(sasbttttuii)','data':[['/bin/evil']]}),empty,empty],
                 [json.dumps({'type':'a(sasbttttuii)','data':None}),empty,empty],
                 ['{"type":"a(sasbttttuii)","data":[],"data":[]}',empty,empty],
                 ['{malformed',empty,empty]]
        for frames in invalid:
            with self.subTest(frames=frames),self.assertRaises((old.core.Refusal,ValueError)):
                old.legacy.effective_unit(self.f.adapter,self.row)


class NativeGPTIdleBinding(old.unittest.TestCase):
    def test_idle_checks_all_active_instances_then_fixed_inactive_metadata(self):
        row=old.legacy.definitions()[1];seen=[]
        adapter=SimpleNamespace(command=lambda args,timeout:'',unit=lambda mid,run:seen.append((mid,run)) or {'ActiveState':'inactive','MainPID':'0'})
        with patch.object(old.legacy,'inspect_name',return_value=None):old.legacy.idle(adapter,row)
        self.assertEqual([(row['id'],'0'*32)],seen)
        adapter.command=lambda args,timeout:'spark-v4-existing-gpt@real-owned.service loaded active running'
        with patch.object(old.legacy,'inspect_name',return_value=None),self.assertRaises(old.core.ObservationError):old.legacy.idle(adapter,row)
        self.assertEqual(1,len(seen),'active real instance must prevent idle qualification')
