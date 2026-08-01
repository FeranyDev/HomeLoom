export function supportsProviderChildDevices(type: string): boolean {
	return type === 'camera' || type === 'mqtt' || type === 'xiaomi' || type === 'xiaomi-miot-cloud'
}
