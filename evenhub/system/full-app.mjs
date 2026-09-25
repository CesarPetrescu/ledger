import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

export const requiredJourneys = [
  'capture.confirm', 'capture.cancel', 'capture.retry-idempotency', 'capture.voice',
  'capture.review-selection', 'capture.cancel-recording',
  'recall.sources', 'recall.degraded', 'recall.empty', 'brief.all-pages', 'brief.checkpoint-restart',
  'calendar.selected', 'calendar.timezones', 'calendar.empty',
]

export async function runFullApp(h) {
  const { step, input, menu, view, capture, admin, sql, until, pendingDevice, decide, marker } = h
  let capturedId, lateId, checkpoint, calendarPage
  const typed = 'Amber telescope typed capture for Atlas.'
  const retryText = 'Amber telescope retry after a lost receipt.'
  const exec = args => execFileSync('xdotool', args, { encoding: 'utf8', timeout: 10000 }).trim()
  async function focusPhone() {
    const ids = exec(['search', '--onlyvisible', '--name', '.']).split('\n')
    const windows = ids.map(id => ({ id, title: exec(['getwindowname', id]) }))
    await writeFile(join(process.env.GLASS_ARTIFACT_DIR, 'native-window-titles.json'), JSON.stringify(windows, null, 2))
    const window = windows.find(w => w.title === 'Browser')
      ?? windows.find(w => /evenhub/i.test(w.title) && !/glasses/i.test(w.title))
      ?? windows.find(w => /ledger glass/i.test(w.title))
    assert(window, 'Could not identify the actual native phone WebView window; inspect native-window-titles.json')
    exec(['windowfocus', '--sync', window.id])
    await delay(150)
  }
  async function typeOnPhone(text) {
    await focusPhone()
    // Real X11 keyboard events reach the actual HTML textarea. No DOM injection,
    // fake bridge, response interception, or test-only production hooks.
    exec(['key', '--clearmodifiers', 'ctrl+a'])
    exec(['type', '--clearmodifiers', '--delay', '4', '--', text])
    exec(['key', '--clearmodifiers', 'ctrl+Return'])
  }
  async function provider(data) {
    const response = await fetch('https://localhost:10443/control', {
      method: data ? 'POST' : 'GET',
      headers: { Authorization: `Bearer ${process.env.GLASS_PROVIDER_TOKEN}`, 'Content-Type': 'application/json' },
      ...(data ? { body: JSON.stringify(data) } : {}), signal: AbortSignal.timeout(10000),
    })
    assert(response.ok, `Provider fault-control returned ${response.status}`)
    return response.json()
  }
  async function draftText(text) {
    const after = marker()
    await menu(4)
    await view('capture-input', () => true, after)
    await typeOnPhone(text)
    const reviewed = await view('capture-review', () => true, after)
    assert.equal(reviewed.pages, 1, 'Short fixture should fit one review page')
    await capture('capture-review')
    await input('click')
    await view('capture-actions', () => true, after)
  }

  await step('capture.cancel', async () => {
    const before = sql('SELECT count(*) FROM entry')
    await draftText('This unconfirmed note must never reach Ledger.')
    const after = marker()
    await input('down', 4); await input('click')
    await view('now', () => true, after)
    assert.equal(sql('SELECT count(*) FROM entry'), before)
    await capture('capture-cancelled-real-db-unchanged')
  })
  await step('capture.confirm', async () => {
    await draftText(typed)
    const after = marker()
    await input('click')
    await view('pairing', () => true, after)
    const device = await pendingDevice()
    assert(device.scope.split(' ').includes('ledger:write'), 'Capture did not request write permission')
    await decide(device.user_code, 'approve', ['ledger:read','ledger:write'])
    capturedId = (await view('capture-saved', () => true, after)).id
    assert.match(capturedId, /^[1-9][0-9]*$/)
    const row = JSON.parse(sql(`SELECT json_build_object('body',body,'source',source) FROM entry WHERE id=${capturedId}`))
    assert.equal(row.body, typed)
    assert.equal(row.source, 'ledger-glass')
    await capture('capture-saved-real-ledger-receipt')
  })
  await step('capture.retry-idempotency', async () => {
    await draftText(retryText)
    await provider({ drop_next_append: true })
    let after = marker()
    await input('click')
    await view('capture-pending', () => true, after)
    const original = sql(`SELECT id FROM entry WHERE body='${retryText}'`)
    assert.match(original, /^[1-9][0-9]*$/)
    assert.equal((await provider()).dropped, 1, 'Actual successful backend response was not dropped')
    await capture('capture-pending-after-real-response-loss')
    after = marker()
    await input('click')
    const receipt = await view('capture-saved', () => true, after)
    assert.equal(receipt.id, original)
    assert.equal(sql(`SELECT count(*) FROM entry WHERE body='${retryText}'`), '1')
    await capture('capture-retry-original-receipt-no-duplicate')
  })
  await step('capture.voice', async () => {
    let after = marker()
    await menu(4); await view('capture-input', () => true, after)
    await input('click'); await view('recording', () => true, after)
    // A generated signal enters the official simulator through PulseAudio, not
    // through a fake SDK callback. Provider output is a labelled STT contract.
    const samples = 16000
    const wav = Buffer.alloc(44 + samples * 2)
    wav.write('RIFF',0); wav.writeUInt32LE(wav.length-8,4); wav.write('WAVEfmt ',8)
    wav.writeUInt32LE(16,16); wav.writeUInt16LE(1,20); wav.writeUInt16LE(1,22)
    wav.writeUInt32LE(16000,24); wav.writeUInt32LE(32000,28); wav.writeUInt16LE(2,32); wav.writeUInt16LE(16,34)
    wav.write('data',36); wav.writeUInt32LE(samples*2,40)
    for (let i=0;i<samples;i++) wav.writeInt16LE(Math.round(7000*Math.sin(2*Math.PI*440*i/16000)),44+i*2)
    const file = join(process.env.GLASS_RUNTIME_DIR,'audio-boundary.wav')
    await writeFile(file,wav)
    await delay(250)
    execFileSync('paplay',['--device=glassci',file],{timeout:10000,stdio:'pipe'})
    await delay(250)
    await input('click')
    await view('capture-review', () => true, after)
    const receipt = await provider()
    assert(receipt.speech_requests.some(r => r.pcm_bytes >= 3200 && r.has_signal), 'No actual simulator microphone signal reached the STT adapter')
    await capture('voice-transcript-review')
    await input('click'); await view('capture-actions', () => true, after)
    after = marker(); await input('click')
    const saved = await view('capture-saved', () => true, after)
    assert.equal(sql(`SELECT body FROM entry WHERE id=${saved.id}`), 'Amber telescope voice capture for Atlas.')
    await capture('voice-capture-confirmed')
  })
  await step('capture.review-selection', async () => {
    await draftText('Amber telescope selection review.')
    let after = marker()
    await input('down',1); await input('click')
    await view('capture-project', () => true, after)
    await input('click') // first actual project: ci-focus
    await view('capture-review', v => v.slug === 'ci-focus', after)
    await input('click'); await view('capture-actions', () => true, after)
    after = marker()
    await input('down',2); await input('click')
    await view('capture-kind', () => true, after)
    await input('down',1); await input('click') // todo
    await view('capture-review', v => v.kind === 'todo', after)
    await input('click'); await view('capture-actions', () => true, after)
    after = marker()
    await input('down',3); await input('click') // edit on phone
    await view('capture-input', () => true, after)
    const body = 'Amber telescope long review. '.repeat(24).trim()
    const before = sql('SELECT count(*) FROM entry')
    await typeOnPhone(body)
    const reviewed = await view('capture-review', () => true, after)
    assert(reviewed.pages > 1)
    for (let index=0;index<reviewed.pages;index++) {
      after = marker(); await input('click')
      if (index+1 < reviewed.pages) await view('capture-review', v => v.page === index+1, after)
      else await view('capture-actions', () => true, after)
      assert.equal(sql('SELECT count(*) FROM entry'),before,'Review navigation submitted a write')
    }
    await capture('long-review-confirmation-actions')
    after = marker(); await input('click')
    const saved = await view('capture-saved', () => true, after)
    const row = JSON.parse(sql(`SELECT json_build_object('body',body,'slug',slug,'kind',kind) FROM entry WHERE id=${saved.id}`))
    assert.deepEqual(row,{body,slug:'ci-focus',kind:'todo'})
    await capture('selected-project-todo-saved')
  })
  await step('capture.cancel-recording', async () => {
    const before = sql('SELECT count(*) FROM entry')
    const requests = (await provider()).speech_requests.length
    const after = marker()
    await menu(4); await view('capture-input', () => true, after)
    await input('click'); await view('recording', () => true, after)
    await delay(300)
    await input('double_click')
    await view('now', () => true, after)
    assert.equal(sql('SELECT count(*) FROM entry'),before)
    assert.equal((await provider()).speech_requests.length,requests,'Cancelled recording was transcribed')
    await capture('recording-cancelled-without-write')
  })
  await step('recall.sources', async () => {
    // Wait for the real indexer to process the captured entry, not a seeded chunk.
    await until(() => Number(sql(`SELECT count(*) FROM chunk WHERE ref='entry:${capturedId}'`)) > 0, 'real captured entry indexed', 60000)
    const after = marker()
    await menu(5); await view('recall-input', () => true, after)
    await typeOnPhone('Amber telescope')
    const result = await view('recall-results', () => true, after)
    const index = result.refs.indexOf(`entry:${capturedId}`)
    assert(index >= 0, 'Actual search did not retrieve the captured entry')
    await capture('recall-real-search-results')
    await input('down',index); await input('click')
    const source = await view('recall-source', r => r.id === capturedId, after)
    assert.equal(source.ref, `entry:${capturedId}`)
    assert.equal(Date.parse(source.created_at), Date.parse(sql(`SELECT created_at FROM entry WHERE id=${capturedId}`)))
    await capture('recall-original-source-and-timestamp')
  })
  await step('recall.degraded', async () => {
    const after = marker()
    await menu(5); await view('recall-input', () => true, after)
    await typeOnPhone('Amber telescope')
    const result = await view('recall-results', () => true, after)
    assert(result.degraded.includes('vector'), 'Inference-unavailable state was not reported')
    assert(result.refs.includes(`entry:${capturedId}`), 'Lexical fallback lost the real source')
    await capture('recall-lexical-fallback')
  })
  await step('recall.empty', async () => {
    const after = marker()
    await menu(5); await view('recall-input', () => true, after)
    await typeOnPhone('zzqvxbnmpwljk nonexistentsource')
    await view('recall-empty', () => true, after)
    await capture('recall-no-matching-source')
  })
  await step('brief.all-pages', async () => {
    for (let i=0;i<35;i++) await admin('/projects/ci-focus/entries','POST',{kind:'note',body:`Brief real entry ${i}`})
    let after = marker()
    await menu(6)
    let page = await view('brief', () => true, after)
    assert.equal(page.checkpoint,'0','Opening Brief must not acknowledge entries')
    checkpoint = page.through
    assert.match(checkpoint,/^[0-9]+$/)
    const expected = sql(`SELECT entry_id FROM entry_change WHERE change_id<=${checkpoint} ORDER BY change_id`).split('\n')
    lateId = String((await admin('/projects/ci-focus/entries','POST',{kind:'note',body:'Arrived after the frozen Brief snapshot'})).id)
    const received = []
    let pages = 0
    for (;;) {
      pages++; received.push(...page.ids)
      await capture(`brief-page-${pages}`)
      after = marker()
      await input('down',page.ids.length); await input('click')
      await view('brief-acknowledged', () => true, after)
      if (!page.has_more) { await view('brief-done', v => v.checkpoint===checkpoint, after); break }
      page = await view('brief', () => true, after)
    }
    assert(pages > 1)
    assert.deepEqual(received,expected,'Brief omitted, duplicated or reordered real changes')
    assert(!received.includes(lateId),'A later write leaked into the frozen snapshot')
  })
  await step('brief.checkpoint-restart', async () => {
    const clients = sql('SELECT count(*) FROM oauth_client')
    const after = marker()
    await focusPhone(); exec(['key','--clearmodifiers','ctrl+alt+r'])
    await view('ready', () => true, after)
    await view('now', () => true, after)
    assert.equal(sql('SELECT count(*) FROM oauth_client'),clients,'Reload silently created a new reader identity')
    await menu(6)
    const resumed = await view('brief', () => true, after)
    assert.equal(resumed.checkpoint,checkpoint)
    assert.deepEqual(resumed.ids,[lateId],'Reload skipped an unread entry or replayed acknowledged pages')
    await capture('brief-resumed-after-webview-restart')
  })
  await step('calendar.selected', async () => {
    const flow = await admin('/calendar/connect','POST',{server_url:'https://glass-provider:9444'})
    const connected = await admin(`/calendar/connect/${flow.id}/poll`,'POST',{})
    assert(connected.connected)
    const available = (await admin('/calendar/calendars')).calendars
    const work = available.find(c=>c.name==='Work')
    assert(work && available.some(c=>c.name==='Hidden'))
    await admin('/calendar/calendars','PUT',{ids:[work.id]})
    const after = marker()
    await menu(7)
    await view('pairing', () => true, after)
    const device = await pendingDevice()
    assert(device.scope.split(' ').includes('calendar:read'))
    assert(!device.scope.split(' ').includes('calendar:write'))
    await decide(device.user_code,'approve',['ledger:read','ledger:write','calendar:read'])
    calendarPage = await view('calendar', () => true, after)
    assert.equal(calendarPage.ids.length,3)
    assert(calendarPage.calendars.every(id=>id===work.id))
    const providerState = await provider()
    assert(providerState.caldav_queries.length > 0)
    assert(providerState.caldav_queries.every(path=>path.endsWith('/work/')),'Unselected calendar was queried')
    await capture('next-owner-selected-calendar')
  })
  await step('calendar.timezones', async () => {
    assert.equal(calendarPage.zone,'Europe/Bucharest')
    let utc, bucharest
    for (const suffix of ['all-day.ics','utc.ics','bucharest.ics']) {
      const index = calendarPage.ids.findIndex(id=>Buffer.from(id,'base64url').toString().endsWith(suffix))
      assert(index>=0,`Missing real CalDAV event ${suffix}`)
      const after = marker()
      await input('down',index); await input('click')
      const shown = await view('calendar-event', () => true, after)
      assert.equal(shown.zone,'Europe/Bucharest')
      if (suffix==='all-day.ics') { assert(shown.all_day); assert.match(shown.start,/^\d{4}-\d{2}-\d{2}$/) }
      else if (suffix==='utc.ics') utc=Date.parse(shown.start)
      else bucharest=Date.parse(shown.start)
      await capture(`calendar-${suffix}`)
      for(let p=0;p<shown.pages;p++) await input('click')
      calendarPage=await view('calendar', () => true, after)
    }
    assert.equal(utc,bucharest,'UTC and Bucharest fixtures did not represent the same instant')
    const receipt = await provider()
    assert.deepEqual(receipt.errors,[],'External provider rejected a contract')
    await writeFile(join(process.env.GLASS_ARTIFACT_DIR,'provider-contract-receipt.json'),JSON.stringify(receipt,null,2))
    // Leave a normal foundation page for the existing restart/outage journeys.
    const after = marker(); await menu(0); await view('now', () => true, after)
  })
  await step('calendar.empty', async () => {
    await admin('/calendar/calendars','PUT',{ids:[]})
    const queries = (await provider()).caldav_queries.length
    const after = marker()
    await menu(7); await view('calendar-empty', () => true, after)
    assert.equal((await provider()).caldav_queries.length,queries,'Unselected calendars were queried')
    await capture('next-no-selected-events')
    const beforeHome = marker(); await menu(0); await view('now', () => true, beforeHome)
  })
}
