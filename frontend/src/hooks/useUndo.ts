import { api, describeError } from '../api'
import { useToast } from '../components/Toast'

/** Shows a success toast with an Undo button for one recorded action. */
export function useUndo() {
  const toast = useToast()
  return (message: string, actionId: string) =>
    toast(message, 'success', {
      label: 'Undo',
      run: () => {
        api.undoAction(actionId).then(
          () => toast('Undone.'),
          (failure: unknown) => toast(describeError(failure), 'error'),
        )
      },
    })
}
