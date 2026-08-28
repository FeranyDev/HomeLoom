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
		<li><span>01</span><div><strong>选择设备来源</strong><small>{source}</small></div></li>
		<li><span>02</span><div><strong>确认统一模型与来源配置</strong><small>{model}</small></div></li>
		<li><span>03</span><div><strong>加入草稿并保存设备</strong><small>{configuration}</small></div></li>
	</ol>
}
