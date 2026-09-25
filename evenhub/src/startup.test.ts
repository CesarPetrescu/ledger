import { describe, it, expect } from 'vitest'
import { CreateStartUpPageContainer, TextContainerProperty, StartUpPageCreateResult } from '@evenrealities/even_hub_sdk'
import { initializePage } from './startup'

const page = () => new CreateStartUpPageContainer({ containerTotalNum: 1, textObject: [
  new TextContainerProperty({ containerID: 1, containerName: 'main', xPosition: 0, yPosition: 0, width: 576, height: 288, isEventCapture: 1, content: 'Ledger', textColor: 4 }),
] })
// Only the native page lifecycle boundary is doubled. The system suite exercises
// the actual same-process WebView reload and backend checkpoint recovery.
describe('Native startup acknowledgement', () => {
  it('uses the successful startup without an unnecessary rebuild', async () => {
    let rebuilt = false
    expect(await initializePage({ createStartUpPageContainer: async () => StartUpPageCreateResult.success,
      rebuildPageContainer: async () => { rebuilt = true; return true },
    }, page())).toBe('created')
    expect(rebuilt).toBe(false)
  })
  it('recovers a retained native page only after a real rebuild acknowledgement', async () => {
    let rebuilt = false
    expect(await initializePage({ createStartUpPageContainer: async () => StartUpPageCreateResult.invalid,
      rebuildPageContainer: async layout => { rebuilt = true; expect(layout.textObject?.[0].content).toBe('Ledger'); return true },
    }, page())).toBe('rebuilt')
    expect(rebuilt).toBe(true)
  })
  it('fails when the host rejects both startup and rebuild', async () => {
    await expect(initializePage({ createStartUpPageContainer: async () => StartUpPageCreateResult.invalid,
      rebuildPageContainer: async () => false,
    }, page())).rejects.toThrow('code 1')
  })
  it('does not hide out-of-memory or oversize failures', async () => {
    for (const result of [StartUpPageCreateResult.outOfMemory, StartUpPageCreateResult.oversize]) {
      let rebuilt = false
      await expect(initializePage({ createStartUpPageContainer: async () => result,
        rebuildPageContainer: async () => { rebuilt = true; return true },
      }, page())).rejects.toThrow(`code ${result}`)
      expect(rebuilt).toBe(false)
    }
  })
})
