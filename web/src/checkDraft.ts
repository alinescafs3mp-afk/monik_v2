// This builder is shared by Save and Trial. It contains no execution or cURL parser.
export type CheckForm = {
 id:string; serviceID:string; url:string; path:string; dial:string; host:string; sni:string;
 method:string; kind:string; purpose:string; interval:number; timeout:number; headers:string;
 body:string; allowPOST:boolean; expectStatus:string; expectText:string; jsonPath:string;
 jsonValue:string; jsonType:string; expectHealth:boolean; latency:string;
 secretID:string; secretHeader:string; bodySecretID:string; insecureTLS:boolean; paused:boolean; ignored:boolean;
};
export function loadCheck(d:any={},service:any={}):CheckForm {return {
 id:d.id||'',serviceID:service.id||d.service_id||'',url:d.url||service.url||'',path:d.path||'',
 dial:d.dial_target||service.dial_target||'',host:d.host_header||service.host_header||'',sni:d.tls_server_name||service.discovery?.tls_server_name||'',
 method:d.method||'GET',kind:d.kind==='baseline_http'||!d.kind?'baseline_http':'http_health',purpose:d.purpose||'responsiveness',
 interval:d.interval_seconds||5,timeout:d.timeout_seconds||2,headers:JSON.stringify(d.headers||{},null,2),body:d.body||'',allowPOST:!!d.allow_post,
 expectStatus:(d.expected_status?.length?d.expected_status:[200]).join(','),expectText:d.expect_text||'',jsonPath:d.expect_json_path||'',
 jsonValue:d.expect_json_value||'',jsonType:d.expect_json_type||'',expectHealth:!!d.expect_health,latency:d.latency_ms==null?'':String(d.latency_ms),
 secretID:d.secret_id||'',secretHeader:d.secret_header||'',bodySecretID:d.body_secret_id||'',insecureTLS:!!d.insecure_tls,paused:!!d.paused,ignored:!!d.ignored,
};}
export function buildCheck(f:CheckForm,id:string):Record<string,unknown> {
 if(!f.serviceID||!f.url.trim())throw Error('Выберите сервис и URL.');
 let u:URL;try{u=new URL(f.url);}catch{throw Error('Укажите полный HTTP(S) URL.');}
 if(!['http:','https:'].includes(u.protocol)||u.username||u.password||u.hash)throw Error('Нужен HTTP(S) URL без пароля и фрагмента.');
 let headers:unknown;try{headers=JSON.parse(f.headers||'{}');}catch{throw Error('Заголовки должны быть JSON-объектом.');}
 if(!headers||Array.isArray(headers)||typeof headers!=='object'||Object.values(headers).some(v=>typeof v!=='string'))throw Error('Значения заголовков должны быть строками.');
 if(Object.keys(headers).length>16)throw Error('Допустимо не более 16 заголовков.');
 if(!Number.isInteger(Number(f.interval))||f.interval<5||f.interval>3600||f.interval%5)throw Error('Интервал: 5–3600 секунд, шаг 5.');
 if(!Number.isInteger(Number(f.timeout))||f.timeout<1||f.timeout>30||f.timeout>=f.interval)throw Error('Таймаут: 1–30 секунд и меньше интервала.');
 if(f.method==='POST'&&!f.allowPOST)throw Error('Подтвердите, что POST используется для безопасной периодической проверки.');
 if(f.method!=='POST'&&(f.body||f.bodySecretID))throw Error('Тело запроса доступно только для явно разрешённого POST.');
 if(f.body&&f.bodySecretID)throw Error('Выберите открытое тело или секретное, не оба.');
 if(new TextEncoder().encode(f.body).length>16384)throw Error('Тело не должно превышать 16 КиБ.');
 const app=f.kind!=='baseline_http';const expected=f.expectStatus.split(',').filter(s=>s.trim()).map(Number);
 if(app&&(!expected.length||expected.some(n=>!Number.isInteger(n)||n<100||n>599)))throw Error('Нужны ожидаемые HTTP-коды от 100 до 599.');
 if(app&&f.method==='HEAD'&&(f.expectText||f.jsonPath||f.expectHealth))throw Error('HEAD не возвращает тело: измените метод или условия.');
 if(f.jsonType==='boolean'&&!['true','false'].includes(f.jsonValue))throw Error('Логическое значение: true или false.');
 const out:Record<string,unknown>={id:f.id||id,service_id:f.serviceID,request_version:1,url:f.url.trim(),path:f.path.trim(),dial_target:f.dial.trim(),host_header:f.host.trim(),tls_server_name:f.sni.trim(),method:f.method,kind:f.kind,purpose:f.purpose,origin:'user',interval_seconds:Number(f.interval),timeout_seconds:Number(f.timeout),headers,allow_post:f.method==='POST'&&f.allowPOST,body:f.body,body_secret_id:f.bodySecretID,secret_id:f.secretID,secret_header:f.secretID?f.secretHeader:'',insecure_tls:f.insecureTLS,paused:f.paused,ignored:f.ignored,expected_status:app?expected:[],expect_text:app?f.expectText:'',expect_json_path:app?f.jsonPath:'',expect_json_value:app?f.jsonValue:'',expect_json_type:app&&f.jsonPath?f.jsonType:'',expect_health:app&&f.expectHealth};
 if(app&&f.latency!==''){const n=Number(f.latency);if(!Number.isInteger(n)||n<1||n>30000)throw Error('Порог времени: 1–30000 мс.');out.latency_ms=n;}
 return out;
}
export function applyPreset(f:CheckForm,preset:string):CheckForm {
 const base={...f,method:'GET',allowPOST:false,body:'',bodySecretID:'',kind:'http_health',expectStatus:'200',expectText:'',jsonPath:'',jsonValue:'',jsonType:'',expectHealth:false,purpose:'readiness'};
 switch(preset){
 case 'ready':return {...base,path:'/readyz',expectHealth:true};
 case 'health':return {...base,path:'/health',expectHealth:true,purpose:'application'};
 case 'spring':return {...base,path:'/actuator/health',jsonPath:'status',jsonType:'string',jsonValue:'UP',purpose:'application'};
 case 'http200':return {...base,path:'/health'};
 case 'rpc':return {...base,interval:Math.max(30,f.interval),method:'POST',path:'/',headers:JSON.stringify({'Content-Type':'application/json'},null,2),body:'{"jsonrpc":"2.0","id":1,"method":"health","params":[]}',jsonPath:'result.status',jsonType:'string',jsonValue:'ok',purpose:'application'};
 default:return f;
 }
}
export function serviceHint(s:any):string {
 if(s?.state==='pending')return 'Ожидаем ответ для актуальной конфигурации. Предыдущий ответ не подтверждает новый запрос.';
 if(['paused','ignored','not_monitored'].includes(s?.state))return 'Регулярная проверка выключена. Предыдущий ответ не является текущим состоянием.';
 const o=s?.observation||s||{};if(o.transport==='refused')return 'TCP-соединение отклонено до отправки HTTP. Проверьте слушающий процесс, адрес, IPv4/IPv6, порт контейнера. Другой путь или тело запроса это не исправят.';
 if(o.transport==='tls_error')return 'TLS не подтверждён. Проверьте имя сервера и доверие сертификату. Автоподбор не отключает проверку TLS.';
 if(o.transport==='timeout')return 'Истёк таймаут. Проверьте доступность и стоимость запроса; увеличивайте таймаут осмысленно.';
 if([401,403].includes(o.http_status))return 'HTTP отвечает, но требует разрешение. Выберите секрет для этого сервиса; пароль не подбирается.';
 if(o.http_status===404)return 'HTTP отвечает, но маршрут не найден. Задайте health-путь и при необходимости Host/SNI.';
 if(o.http_status===405)return 'Маршрут не принимает этот HTTP-метод. Укажите документированный метод сервиса.';
 if(o.app_result==='fail')return 'Соединение есть, но условия проверки не выполнены. Сравните ответ с ожидаемыми значениями.';
 return '';
}

export function requestPreview(f:CheckForm):string {
 try {const u=new URL(f.url);if(f.path){const p=new URL(f.path,u.origin);u.pathname=p.pathname;u.search=p.search;}u.username='';u.password='';u.hash='';return `${f.method} ${u.toString()}`;}catch{return 'URL пока некорректен';}
}

// Fleet summaries can require attention while the single target is still offline.
// Only per-target terminal evidence ends a check-editor wait.
export function checkOperationFeedback(op:any):{done:boolean;message:string} {
 const targets=Array.isArray(op?.targets)?op.targets:[];
 const terminal=new Set(['succeeded','rejected','failed','rolled_back','unsupported','expired','cancelled_before_execution','unknown_result']);
 if(!targets.length||!targets.every((t:any)=>terminal.has(t.status)))return {done:false,message:targets.map((t:any)=>t.message).filter(Boolean).join('; ')||'Ожидаем выполнения агентом'};
 const success=targets.every((t:any)=>t.status==='succeeded');
 if(!success)return {done:true,message:'Операция не подтверждена успешно: '+targets.map((t:any)=>t.message||t.status).join('; ')};
 if(op.action==='check.trial'&&!targets.every((t:any)=>t.evidence?.trial))return {done:true,message:'Агент завершил задание, но данные пробного запроса отсутствуют. Проверьте операцию.'};
 return {done:true,message:op.action==='check.trial'?'Пробный запрос завершён. Результат ниже; определение не сохранено.':'Агент подтвердил применение проверки.'};
}
