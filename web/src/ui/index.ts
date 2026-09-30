// Relay UI kit — import from '@/ui' (or '../../ui'). Documented in
// docs/dev/UI_KIT.md; every component has a story on /dev/ui.
// Heavy dependencies (marked, DOMPurify, highlight.js, qrcode-generator)
// load lazily inside Markdown / CodeBlock / QR, so importing this barrel
// does not pull them into the initial bundle.

export { AgentMark, type AgentMarkProps } from './AgentMark'
export { Button, type ButtonProps, type ButtonSize, type ButtonVariant } from './Button'
export { CodeBlock, type CodeBlockProps } from './CodeBlock'
export { CompactBar, type CompactBarProps } from './CompactBar'
export { ConfirmButton, type ConfirmButtonProps } from './ConfirmButton'
export { Dialog, type DialogProps } from './Dialog'
export { DotMeter, type DotMeterProps } from './DotMeter'
export { DotText, type DotTextProps } from './DotText'
export {
  Checkbox,
  type CheckboxProps,
  Field,
  type FieldProps,
  Input,
  type InputProps,
  Segmented,
  type SegmentedOption,
  type SegmentedProps,
  Select,
  type SelectOption,
  type SelectProps,
  Slider,
  type SliderProps,
  Switch,
  type SwitchProps,
  TextArea,
  type TextAreaProps,
} from './form'
export { Glyph, type GlyphProps, RelayMark } from './Glyph'
export { GLYPHS, type GlyphName } from './glyphs'
export { Icon, type IconComponent, type IconProps, registerIcons } from './Icon'
export {
  Badge,
  type BadgeProps,
  Card,
  type CardProps,
  type Column,
  EmptyState,
  type EmptyStateProps,
  Kbd,
  List,
  ListRow,
  type ListRowProps,
  Panel,
  type PanelProps,
  PathBar,
  type PathBarProps,
  Progress,
  ProgressRing,
  Skeleton,
  type TabItem,
  Table,
  type TableProps,
  Tabs,
  Tag,
} from './layout'
export { Markdown, type MarkdownProps } from './Markdown'
export { Menu, MenuButton, type MenuItem, type MenuProps, SEPARATOR, useContextMenu, useLongPress } from './Menu'
export { type Anchor, type Placement, Portal, useMedia } from './overlay'
export { Popover, type PopoverProps, Tooltip } from './Popover'
export { QR, type QRProps } from './QR'
export { Sheet, type SheetProps } from './Sheet'
export { Sparkline, type SparklineProps } from './Sparkline'
export { Spinner } from './Spinner'
export { Splitter, type SplitterProps } from './Splitter'
export { type Status, StatusDot, type StatusDotProps, statusLabel, statusOf } from './StatusDot'
export { dismissToast, type ToastKind, type ToastOptions, Toaster, toast, toasts } from './Toast'
export { VirtualList, type VirtualListHandle, type VirtualListProps } from './VirtualList'
