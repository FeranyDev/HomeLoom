type ProviderDeviceAddFlowProps = {
	source: string
	model: string
	configuration: string
}

/**
 * The common, provider-agnostic contract for adding devices. Individual
 * providers may discover a cloud directory, scan the LAN, or require manual
 * connection details, but they all stage a device first and apply the whole
 * catalog with the same final save action.
 */
export function ProviderDeviceAddFlow({ source, model, configuration }: ProviderDeviceAddFlowProps) {
	return <ol className="provider-device-add-flow" aria-label="统一设备添加流程">
		<li><span>01</span><div><strong><HelpTooltip content={source} label="设备来源说明">选择设备</HelpTooltip></strong></div></li>
		<li><span>02</span><div><strong><HelpTooltip content={model} label="统一模型说明">确认模型</HelpTooltip></strong></div></li>
		<li><span>03</span><div><strong><HelpTooltip content={configuration} label="保存草稿说明">加入草稿</HelpTooltip></strong></div></li>
	</ol>
}
import { HelpTooltip } from './HelpTooltip'
