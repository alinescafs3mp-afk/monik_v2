/** Ephemeral terminal framing. No storage, retries, or application operation queue. */
export const MAX_TERMINAL_BACKLOG = 128 * 1024;
export function inputChunks(text: string): string[] {
  if (typeof text !== 'string' || text.length > 65536) throw new Error('Слишком большой ввод: предел 64 КиБ за одно действие.');
  const chunks:string[]=[]; let part='',size=0;
  for (const char of text) {
    const bytes=new TextEncoder().encode(char).length;
    if(size+bytes>4096){chunks.push(part);part='';size=0;}
    part+=char;size+=bytes;
  }
  if(part)chunks.push(part);return chunks;
}
export function authFrame(transport: 'agent'|'ssh', ticket:string, cols:number, rows:number, credentials:{password:string;private_key:string;passphrase:string}) {
  const base={type:'authenticate',ticket,cols:Math.min(300,Math.max(20,cols)),rows:Math.min(120,Math.max(5,rows))};
  return transport==='agent'?base:{...base,...credentials};
}
export function terminalOutput(data:unknown):Uint8Array {
  if(typeof data!=='string'||data.length>10924)throw new Error('Превышен размер вывода терминала.');
  const bytes=Uint8Array.from(atob(data),c=>c.charCodeAt(0));
  if(bytes.length>8192)throw new Error('Превышен размер вывода терминала.');return bytes;
}
