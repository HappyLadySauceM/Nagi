import { createRoot } from 'react-dom/client'
import { ConfirmDialog } from './confirm-dialog'
/** Show the shared confirmation UI for imperative actions.
 * 为命令式操作显示统一的确认界面。 */
export function confirmAction(description: string): Promise<boolean> {
  return new Promise((resolve) => {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    let settled = false
    const finish = (value: boolean) => {
      if (settled) return
      settled = true
      resolve(value)
      queueMicrotask(() => {
        root.unmount()
        host.remove()
      })
    }
    root.render(
      <ConfirmDialog
        open
        onOpenChange={(open) => {
          if (!open) finish(false)
        }}
        title="确认操作"
        description={description}
        confirmLabel="确认"
        cancelLabel="取消"
        onConfirm={() => finish(true)}
      />,
    )
  })
}
