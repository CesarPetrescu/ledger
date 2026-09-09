import { describe, it, expect } from 'vitest'
import { CaptureOutbox, pcmToWav, base64, textPages, formatCalendarEvent, type CaptureDraft } from './features'

class StorageBoundary {
  values = new Map<string,string>()
  refuse = false
  async getLocalStorage(key: string) { return this.values.get(key) ?? '' }
  async setLocalStorage(key: string, value: string) { if (this.refuse) return false; this.values.set(key,value); return true }
}
const draft = (): CaptureDraft => ({ key: 'capture-test-0001', slug: 'atlas', kind: 'note', body: 'Ședință pentru Atlas. Check retries.', confirmed: false })

describe('Capture durable intent', () => {
  it('does not cross the write boundary before explicit confirmation', async () => {
    const storage = new StorageBoundary(); const outbox = new CaptureOutbox(storage,'https://example.com')
    let writes=0
    await expect(outbox.submit(draft(),'device',async()=>{writes++;return{id:1}})).rejects.toThrow('explicit confirmation')
    expect(writes).toBe(0)
  })
  it('persists confirmed intent before a network write and keeps the identical key after failure', async () => {
    const storage = new StorageBoundary(); const outbox = new CaptureOutbox(storage,'https://example.com')
    const pending = {...draft(), confirmed:true,clientId:'device'}
    await expect(outbox.submit(pending,'device',async received=>{
      expect(await outbox.load()).toEqual(received)
      throw new Error('Response lost')
    })).rejects.toThrow('Response lost')
    const restored = await new CaptureOutbox(storage,'https://example.com').load()
    expect(restored).toEqual(pending)
    await outbox.submit(restored!,'device',async received=>{expect(received.key).toBe(pending.key);return{id:'12'}})
    expect(await outbox.load()).toBeNull()
  })
  it('rejects cross-identity automatic replay and host persistence refusal', async () => {
    const storage = new StorageBoundary(); const outbox = new CaptureOutbox(storage,'https://example.com')
    const pending={...draft(),confirmed:true,clientId:'original'}
    let writes=0
    const write=async()=>{writes++;return{id:1}}
    await expect(outbox.submit(pending,'different',write)).rejects.toThrow('another device')
    storage.refuse=true
    await expect(outbox.submit(pending,'original',write)).rejects.toThrow('could not be stored')
    expect(writes).toBe(0)
  })
  it('never calls an unacknowledged write saved and isolates server origins', async () => {
    const storage = new StorageBoundary(); const outbox = new CaptureOutbox(storage,'https://example.com')
    const pending={...draft(),confirmed:true,clientId:'device'}
    await expect(outbox.submit(pending,'device',async()=>({id:0}))).rejects.toThrow('did not acknowledge')
    expect(await outbox.load()).toEqual(pending)
    expect(await new CaptureOutbox(storage,'https://other.example.com').load()).toBeNull()
  })
  it('cancels an unconfirmed draft without writing anything', async () => {
    const outbox=new CaptureOutbox(new StorageBoundary(),'https://example.com')
    await outbox.save(draft()); await outbox.discard();expect(await outbox.load()).toBeNull()
  })
})

describe('Microphone encoding',()=>{
  it('encodes actual PCM samples as canonical 16kHz mono 16-bit RIFF',()=>{
    const pcm=new Uint8Array(3200); new DataView(pcm.buffer).setInt16(0,-1000,true)
    const wav=pcmToWav([pcm.subarray(0,1600),pcm.subarray(1600)])
    const view=new DataView(wav.buffer)
    expect(new TextDecoder().decode(wav.subarray(0,4))).toBe('RIFF')
    expect(view.getUint32(24,true)).toBe(16000);expect(view.getUint16(22,true)).toBe(1)
    expect(view.getInt16(44,true)).toBe(-1000)
    expect(Buffer.from(base64(wav),'base64')).toEqual(Buffer.from(wav))
  })
  it('bounds duration and rejects partial PCM sample pairs',()=>{
    for(const length of [0,3198,3201,960002])expect(()=>pcmToWav([new Uint8Array(length)])).toThrow()
    expect(pcmToWav([new Uint8Array(960000)]).length).toBe(960044)
  })
})

describe('Complete text and calendar rendering',()=>{
  it('paginates without discarding Romanian, English or long technical identifiers',()=>{
    const text='Ședință despre PostgreSQL. '.repeat(50)+'x'.repeat(120)
    const pages=textPages(text)
    expect(pages.length).toBeGreaterThan(1)
    expect(pages.join('').replace(/\s/g,'')).toBe(text.replace(/\s/g,''))
    for(const page of pages)for(const line of page.split('\n'))expect([...line].length).toBeLessThanOrEqual(42)
  })
  it('renders equal instants identically and does not shift all-day DATE values',()=>{
    const event={id:'1',calendar_id:'a',calendar_name:'Work',title:'Atlas',start:'2026-09-09T23:30:00Z',end:'2026-09-10T00:30:00Z',all_day:false}
    const utc=formatCalendarEvent(event,'Europe/Bucharest')
    const offset=formatCalendarEvent({...event,start:'2026-09-10T02:30:00+03:00'},'Europe/Bucharest')
    expect(offset).toBe(utc);expect(utc).toContain('10 Sept 2026');expect(utc).toContain('02:30')
    const allDay=formatCalendarEvent({...event,start:'2026-09-10',end:'2026-09-11',all_day:true},'America/Los_Angeles')
    expect(allDay).toContain('2026-09-10 · All day');expect(allDay).toContain('2026-09-11 (exclusive)')
  })
})
