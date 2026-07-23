import type { DeviceType } from '../types/device'

export function DeviceTypeIcon({ type }: { type: DeviceType }) {
	return <span className={`device-type-icon is-${type}`} aria-hidden="true">
		<svg viewBox="0 0 48 48" focusable="false">
			{type === 'lightbulb' ? <><path d="M16 20a8 8 0 1 1 16 0c0 4-3 5-4 9h-8c-1-4-4-5-4-9Z" /><path d="M20 33h8M21 37h6" /></>
				: type === 'outlet' || type === 'switch' ? <><rect x="11" y="9" width="26" height="30" rx="7" /><path d="M20 20v5M28 20v5M20 32h8" /></>
				: type === 'temperature-humidity-sensor' || type === 'temperature-sensor' || type === 'humidity-sensor' ? <><circle cx="24" cy="24" r="15" /><path d="M18 28V17a3 3 0 0 1 6 0v11a5 5 0 1 1-6 0ZM31 18h3M31 24h3" /></>
				: type === 'contact-sensor' ? <><rect x="10" y="11" width="18" height="27" rx="5" /><rect x="32" y="16" width="6" height="17" rx="2" /></>
				: type === 'motion-sensor' ? <><circle cx="24" cy="24" r="15" /><circle cx="24" cy="24" r="5" /><path d="M11 14c4 3 7 4 13 4s9-1 13-4" /></>
				: type === 'window-covering' ? <><path d="M10 10h28M13 14h22v24H13zM18 14v24M30 14v24" /></>
				: type === 'fan' ? <><circle cx="24" cy="24" r="4" /><path d="M24 20c-5-7-1-12 3-10 4 2 2 8-3 10ZM28 24c7-5 12-1 10 3-2 4-8 2-10-3ZM24 28c5 7 1 12-3 10-4-2-2-8 3-10ZM20 24c-7 5-12 1-10-3 2-4 8-2 10 3Z" /></>
				: <><rect x="14" y="7" width="20" height="34" rx="8" /><circle cx="24" cy="17" r="2" /><path d="M18 27h12M18 31h12M19 35h10" /></>}
		</svg>
	</span>
}
