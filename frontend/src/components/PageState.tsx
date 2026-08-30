import { HelpTooltip } from './HelpTooltip'

export function LoadingState({ message = '正在连接 HomeLoom…' }: { message?: string }) { return <div className="page-state is-loading"><span /><p>{message}</p></div> }
export function CollectionEmpty({ title, description }: { title: string; description: string }) { return <div className="page-state"><strong><HelpTooltip label={`${title}说明`} content={description}>{title}</HelpTooltip></strong></div> }
