export function supportsProviderChildDevices(type: string): boolean {
	return type === 'virtual' || type === 'camera' || type === 'mqtt' || type === 'xiaomi' || type === 'xiaomi-miot-cloud'
}
