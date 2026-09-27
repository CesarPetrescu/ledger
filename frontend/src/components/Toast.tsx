import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'
import { Icon } from './ui'

type ToastKind = 'success' | 'error'

/** An optional button on a toast, such as Undo. */
export interface ToastAction {
  label: string
  run: () => void
}

interface Toast {
  id: number
  kind: ToastKind
  message: string
  action?: ToastAction | undefined
}

type Push = (message: string, kind?: ToastKind, action?: ToastAction) => void

const ToastContext = createContext<Push>(() => {})
let nextId = 0

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const dismiss = useCallback((id: number) => setToasts((current) => current.filter((toast) => toast.id !== id)), [])
  const push = useCallback<Push>(
    (message, kind = 'success', action) => {
      const id = ++nextId
      setToasts((current) => [...current.slice(-2), { id, kind, message, action }])
      // Toasts with an action stay longer so there is time to use it.
      window.setTimeout(() => dismiss(id), action ? 10000 : 6000)
    },
    [dismiss],
  )
  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite" aria-label="Notifications">
        {toasts.map((toast) => (
          <div key={toast.id} className="toast" data-kind={toast.kind}>
            <span>{toast.message}</span>
            {toast.action && (
              <button type="button" className="toast-action" onClick={() => { dismiss(toast.id); toast.action?.run() }}>
                {toast.action.label}
              </button>
            )}
            <button type="button" className="icon-button" aria-label="Dismiss notification" onClick={() => dismiss(toast.id)}>
              <Icon name="close" />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

export function useToast(): Push {
  return useContext(ToastContext)
}
