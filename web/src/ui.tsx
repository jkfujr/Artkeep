import { Button, Input, Label, ListBox, Modal, Select, TextField } from '@heroui/react'
import { X } from 'lucide-react'
import type { ReactNode } from 'react'

export function Field({ label, value, onChange, type = 'text', placeholder, autoFocus, required = false, disabled = false, name }: { label: string; value: string; onChange: (value: string) => void; type?: string; placeholder?: string; autoFocus?: boolean; required?: boolean; disabled?: boolean; name?: string }) {
  return <TextField className="field" isRequired={required} isDisabled={disabled} name={name}>
    <Label>{label}</Label><Input type={type} value={value} onChange={event => onChange(event.target.value)} placeholder={placeholder} autoFocus={autoFocus} autoComplete={type === 'password' ? 'current-password' : undefined} />
  </TextField>
}
export function Choice({ label, value, onChange, options, className = '' }: { label: string; value: string; onChange: (value: string) => void; options: { key: string; label: string }[]; className?: string }) {
  return <Select aria-label={label} className={`choice ${className}`} value={value} onChange={value => { if (value !== null) onChange(String(value)) }}>
    <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
    <Select.Popover><ListBox items={options}>{item => <ListBox.Item id={item.key} textValue={item.label}>{item.label}<ListBox.ItemIndicator /></ListBox.Item>}</ListBox></Select.Popover>
  </Select>
}
export function Dialog({ title, open, onClose, children, footer, wide = false }: { title: string; open: boolean; onClose: () => void; children: ReactNode; footer?: ReactNode; wide?: boolean }) {
  return <Modal><Modal.Backdrop isOpen={open} onOpenChange={value => { if (!value) onClose() }}>
    <Modal.Container size={wide ? 'lg' : 'sm'}><Modal.Dialog className="dialog">
      <Modal.Header><Modal.Heading>{title}</Modal.Heading><Button isIconOnly variant="ghost" aria-label="关闭" onPress={onClose}><X size={19} /></Button></Modal.Header>
      <Modal.Body>{children}</Modal.Body>{footer && <Modal.Footer>{footer}</Modal.Footer>}
    </Modal.Dialog></Modal.Container>
  </Modal.Backdrop></Modal>
}
export function ErrorNotice({ children }: { children: ReactNode }) { return children ? <div className="error-notice" role="alert">{children}</div> : null }
export function Platform({ platform }: { platform: string }) { return <span className={`platform platform-${platform}`}><span className="platform-dot" />{platform === 'pixiv' ? 'Pixiv' : platform === 'local' ? '本地' : 'X'}</span> }
export function Rating({ rating }: { rating: number }) { return rating > 0 ? <span className={`rating rating-${rating}`}>{rating === 2 ? 'R-18G' : 'R-18'}</span> : null }
