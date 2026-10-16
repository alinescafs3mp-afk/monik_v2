"""Exact installed/candidate AST; protocol-shaped objects, no SDK/model imports."""
import ast,copy,itertools,json,re,types,unittest
from pathlib import Path
R=Path(__file__).resolve().parent;E=R/'upstream';OVERRIDES=R.parent/'deployment/opt/spark/model-collection-v4/runtime/gemma4-source-overrides'
class Obj:
 def __init__(self,**kw):self.__dict__.update(kw)
 def model_dump(self,exclude_none=False,**kw):return {k:v for k,v in self.__dict__.items() if not exclude_none or v is not None}
 def model_copy(self,update=None):return type(self)(**dict(self.__dict__,**(update or {})))
class Fn(Obj):
 def __init__(self,name=None,arguments=None,**kw):super().__init__(name=name,arguments=arguments,**kw)
class TC(Obj):
 def __init__(self,index=None,id=None,type=None,function=None,**kw):super().__init__(index=index,id=id,type=type,function=Fn(**function) if isinstance(function,dict) else function,**kw)
class DM(Obj):
 def __init__(self,content=None,tool_calls=None,**kw):super().__init__(content=content,tool_calls=tool_calls,**kw)
class Base:
 def __init__(self,t,tools=None):self.model_tokenizer=t;self.vocab=t.vocab
 def adjust_request(self,r):return r
ids=itertools.count()
def load_parser(path):
 tree=ast.parse(path.read_text());nodes=[n for n in tree.body if isinstance(n,(ast.ClassDef,ast.FunctionDef))]
 ns=dict(ToolParser=Base,TokenizerLike=Obj,Tool=Obj,ChatCompletionRequest=Obj,ResponsesRequest=Obj,ExtractedToolCallInformation=Obj,ToolCall=TC,FunctionCall=Fn,DeltaMessage=DM,DeltaToolCall=TC,DeltaFunctionCall=Fn,logger=types.SimpleNamespace(debug=lambda *a,**kw:None,exception=lambda *a,**kw:None),re=re,json=json,make_tool_call_id=lambda:'offline-'+str(next(ids)),find_common_prefix=lambda a,b:a[:next((i for i,(x,y) in enumerate(zip(a,b)) if x!=y),min(len(a),len(b)))],TOOL_CALL_START='<|tool_call>',TOOL_CALL_END='<tool_call|>',STRING_DELIM='<|"|>')
 module=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[]);exec(compile(ast.fix_missing_locations(module),str(path),'exec'),ns)
 return ns['Gemma4ToolParser'](types.SimpleNamespace(vocab={'<|tool_call>':1,'<tool_call|>':2}))
def stream(path,chunks):
 p=load_parser(path);current='';messages=[]
 for chunk in chunks:
  previous=current;current+=chunk;d=p.extract_tool_calls_streaming(previous,current,chunk,[],[],[],Obj())
  if d is not None:messages.append(d)
 return p,messages

def combine(messages):
 text='';calls={}
 for d in messages:
  text+=d.content or ''
  for t in d.tool_calls or []:
   c=calls.setdefault(t.index,dict(name='',arguments='',ids=[]));c['name']+=t.function.name or '';c['arguments']+=t.function.arguments or ''
   if t.id:c['ids'].append(t.id)
 return text,{i:dict(name=c['name'],arguments=json.loads(c['arguments']),ids=c['ids']) for i,c in calls.items()}
ORIGINAL=E/'gemma4_tool_parser.py';PATCHED=OVERRIDES/'gemma4_tool_parser.py'
A='<|tool_call>call:lookup_record{name:<|"|>primary<|"|>}<tool_call|>'
B='<|tool_call>call:write_file{path:<|"|>x.txt<|"|>,text:<|"|>тест<|"|>,ok:true}<tool_call|>'
C='<|tool_call>call:empty{}<tool_call|>'
def terminal(path,p,delta):
 tree=ast.parse(path.read_text());condition=next(n for n in ast.walk(tree) if isinstance(n,ast.If) and 'should_check' in ast.unparse(n.test) and any(isinstance(x,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='latest_delta_len' for t in x.targets) for x in n.body))
 f=copy.deepcopy(next(n for n in ast.walk(tree) if isinstance(n,ast.FunctionDef) and n.name=='_create_remaining_args_delta'));f.decorator_list=[]
 ns=dict(DeltaMessage=DM,DeltaToolCall=TC,DeltaFunctionCall=Fn,json=json)
 exec(compile(ast.fix_missing_locations(ast.Module(body=[f],type_ignores=[])),str(path),'exec'),ns)
 ns.update(should_check=True,tool_parser=p,auto_tools_called=bool(p.prev_tool_call_arr),delta_message=delta,index=len(p.prev_tool_call_arr)-1,self=types.SimpleNamespace(_create_remaining_args_delta=ns['_create_remaining_args_delta']))
 exec(compile(ast.fix_missing_locations(ast.Module(body=[condition],type_ignores=[])),str(path),'exec'),ns)
 return ns['delta_message']
class ParserControls(unittest.TestCase):
 def test_original_complete_single_delta_real_defect(self):
  p,parts=stream(ORIGINAL,[A]);self.assertEqual(parts,[]);self.assertEqual(p.current_tool_id,-1)
  p,parts=stream(PATCHED,[A]);text,calls=combine(parts);self.assertEqual(calls[0]['name'],'lookup_record');self.assertEqual(calls[0]['arguments'],{'name':'primary'});self.assertEqual(len(calls[0]['ids']),1)
 def test_existing_split_and_nonstream_preserved(self):
  chunks=['<|tool_call>','call:lookup_record{','name:<|"|>primary<|"|>','}','<tool_call|>']
  for source in (ORIGINAL,PATCHED):
   p,parts=stream(source,chunks);self.assertEqual(combine(parts)[1][0]['arguments'],{'name':'primary'})
   non=p.extract_tool_calls(A,Obj());self.assertTrue(non.tools_called);self.assertEqual(json.loads(non.tool_calls[0].function.arguments),{'name':'primary'})
 def test_all_two_way_splits_and_character_chunks_no_drop_or_duplicate(self):
  for cut in range(1,len(A)):
   p,parts=stream(PATCHED,[A[:cut],A[cut:]]);text,calls=combine(parts)
   self.assertEqual(text,'');self.assertEqual(calls[0]['name'],'lookup_record',cut);self.assertEqual(calls[0]['arguments'],{'name':'primary'},cut);self.assertEqual(len(calls[0]['ids']),1)
  p,parts=stream(PATCHED,list(B));self.assertEqual(combine(parts)[1][0]['arguments'],{'path':'x.txt','text':'тест','ok':True})
 def test_multiple_calls_whole_or_crossing_boundaries_and_text(self):
  whole='Before '+A+B+C+' After<div>'
  for chunks in ([whole],['Before '+A+B[:16],B[16:]+C+' After<','div>'],list(whole)):
   p,parts=stream(PATCHED,chunks);text,calls=combine(parts);self.assertEqual(text,'Before  After<div>');self.assertEqual([calls[i]['name'] for i in calls],['lookup_record','write_file','empty']);self.assertEqual(calls[2]['arguments'],{});self.assertEqual(len({c['ids'][0] for c in calls.values()}),3)
 def test_plain_text_and_incomplete_special_prefix(self):
  for text in ('Hello <div> World','No tools','text <|not_a_tool> end'):
   p,parts=stream(PATCHED,list(text));self.assertEqual(combine(parts),(text,{}))
 def test_terminal_companion_original_drops_calls_candidate_preserves(self):
  p,parts=stream(PATCHED,['Before '+A+B+' After']);self.assertEqual(len(parts),1);parts[0].reasoning='kept'
  old=terminal(E/'serving.py',p,parts[0]);self.assertEqual(len(old.tool_calls),1);self.assertIsNone(old.content)
  new=terminal(OVERRIDES/'serving.py',p,parts[0]);self.assertEqual(combine([new]),combine(parts));self.assertEqual(new.reasoning,'kept')
 def test_terminal_single_call_and_incomplete_next_never_fabricates(self):
  for text in (A,A+'<|tool_call>'):
   p,parts=stream(PATCHED,[text]);before=combine(parts);new=terminal(OVERRIDES/'serving.py',p,parts[0]);self.assertEqual(combine([new]),before);self.assertEqual(len(new.tool_calls),1)
 def test_only_parser_entry_and_terminal_catchup_areas_changed(self):
  old=ast.parse(ORIGINAL.read_text());new=ast.parse(PATCHED.read_text());a=next(n for n in old.body if isinstance(n,ast.ClassDef) and n.name=='Gemma4ToolParser');b=next(n for n in new.body if isinstance(n,ast.ClassDef) and n.name=='Gemma4ToolParser')
  fa={n.name:n for n in a.body if isinstance(n,ast.FunctionDef)};fb={n.name:n for n in b.body if isinstance(n,ast.FunctionDef)};self.assertEqual(set(fa),set(fb))
  for name in fa:
   if name!='extract_tool_calls_streaming':self.assertEqual(ast.dump(fa[name]),ast.dump(fb[name]),name)
  for p in (PATCHED,OVERRIDES/'serving.py'):compile(p.read_text(),str(p),'exec')
if __name__=='__main__':unittest.main(verbosity=2)
