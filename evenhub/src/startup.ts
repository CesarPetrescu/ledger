import {
  CreateStartUpPageContainer, RebuildPageContainer, StartUpPageCreateResult,
  formatEvenHubPageContainerValidationError, validateEvenHubPageContainer,
  type EvenAppBridge,
} from '@evenrealities/even_hub_sdk'

type PageHost = Pick<EvenAppBridge, 'createStartUpPageContainer' | 'rebuildPageContainer'>

/** A WebView reload can leave the native page alive. Never treat an error as
 * success: recovery requires a separately acknowledged rebuild of valid input. */
export async function initializePage(host: PageHost, page: CreateStartUpPageContainer): Promise<'created' | 'rebuilt'> {
  const validation = validateEvenHubPageContainer(page)
  if (!validation.valid) throw new Error(formatEvenHubPageContainerValidationError(validation))
  const result = await host.createStartUpPageContainer(page)
  if (result === StartUpPageCreateResult.success) return 'created'
  if (result === StartUpPageCreateResult.invalid) {
    // Code 1 is not assumed to mean "already exists". The identical valid
    // payload must actually rebuild; other errors (OOM/oversize) remain fatal.
    if (await host.rebuildPageContainer(new RebuildPageContainer(page))) return 'rebuilt'
  }
  throw new Error(`Even Hub startup page failed with code ${result}`)
}
