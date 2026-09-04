import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'
import { Icon } from './ui'

type ToastKind = 'success' | 'error'

interface Toast {
  id: number
  kind: ToastKind
  message: string
}

type Push = (message: string, kind?: ToastKind) => void

const ToastContext = createContext<Push>(() => {})
let nextId = 0

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const dismiss = useCallback((id: number) => setToasts((current) => current.filter((toast) => toast.id !== id)), [])
  const push = useCallback<Push>(
    (message, kind = 'success') => {
      const id = ++nextId
      setToasts((current) => [...current.slice(-2), { id, kind, message }])
      window.setTimeout(() => dismiss(id), 6000)
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
