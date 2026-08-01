import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DeviceDetails } from './DeviceDetails'
import type { Device } from '../types/device'

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

beforeEach(() => {
  vi.spyOn(window.HTMLMediaElement.prototype, 'play').mockResolvedValue()
  vi.spyOn(window.HTMLMediaElement.prototype, 'pause').mockImplementation(() => undefined)
  vi.spyOn(window.HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined)
})

describe('DeviceDetails', () => {
  it('renders a property-less camera whose endpoint collection is null', async () => {
    const play = vi.mocked(window.HTMLMediaElement.prototype.play)
    const pause = vi.mocked(window.HTMLMediaElement.prototype.pause)
    class StateSource { addEventListener() {} close() {} }
    vi.stubGlobal('EventSource', StateSource)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [] }) }))
    const camera = {
      schemaVersion: 1, id: 'camera-1', providerId: 'xiaomi-main', name: '客厅摄像头', type: 'camera',
      availability: 'online', online: true, lastUpdateAt: '2026-07-26T00:00:00Z', endpoints: null,
    } as unknown as Device

    render(<DeviceDetails device={camera} onClose={vi.fn()} onPropertyWrite={vi.fn()} onCommandExecute={vi.fn()} />)

    expect(screen.getByRole('dialog', { name: '客厅摄像头详情' })).toBeInTheDocument()
    const preview = screen.getByLabelText('摄像头实时画面') as HTMLVideoElement
    expect(preview).toHaveAttribute('src', '/api/v1/media/devices/camera-1/preview.mp4?attempt=0')
    expect(play).toHaveBeenCalled()
    expect(preview.muted).toBe(true)
    Object.defineProperty(preview, 'buffered', { configurable: true, value: { length: 1, start: () => 0, end: () => 5 } })
    preview.currentTime = 0
    fireEvent.progress(preview)
    expect(preview.currentTime).toBeCloseTo(4.75)
    fireEvent.error(preview)
    expect(screen.getByRole('alert')).toHaveTextContent('实时预览已中断')
    await userEvent.click(screen.getByRole('button', { name: '重新连接' }))
    expect(screen.getByLabelText('摄像头实时画面')).toHaveAttribute('src', '/api/v1/media/devices/camera-1/preview.mp4?attempt=1')
    expect(pause).toHaveBeenCalled()
    expect(screen.getByText('该设备没有可配置的统一属性；摄像头媒体由独立发布器管理。')).toBeInTheDocument()
  })

  it('stops showing an endless camera loader and allows retry after the keyframe deadline', () => {
    vi.useFakeTimers()
    class StateSource { addEventListener() {} close() {} }
    vi.stubGlobal('EventSource', StateSource)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [] }) }))
    const camera = {
      schemaVersion: 1, id: 'camera-timeout', providerId: 'camera-main', name: '门口摄像头', type: 'camera',
      availability: 'online', online: true, lastUpdateAt: '2026-07-26T00:00:00Z', endpoints: [],
    } as Device

    render(<DeviceDetails device={camera} onClose={vi.fn()} onPropertyWrite={vi.fn()} onCommandExecute={vi.fn()} />)
    act(() => vi.advanceTimersByTime(12_000))

    expect(screen.getByRole('alert')).toHaveTextContent('等待关键帧超时')
    fireEvent.click(screen.getByRole('button', { name: '重新连接' }))
    expect(screen.getByLabelText('摄像头实时画面')).toHaveAttribute('src', '/api/v1/media/devices/camera-timeout/preview.mp4?attempt=1')
    expect(screen.getByText('正在向独立媒体发布器请求视频流…')).toBeInTheDocument()
  })

  it('keeps an already playing camera visible while it briefly buffers', () => {
    vi.useFakeTimers()
    class StateSource { addEventListener() {} close() {} }
    vi.stubGlobal('EventSource', StateSource)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [] }) }))
    const camera = {
      schemaVersion: 1, id: 'camera-buffering', providerId: 'camera-main', name: '门口摄像头', type: 'camera',
      availability: 'online', online: true, lastUpdateAt: '2026-07-26T00:00:00Z', endpoints: [],
    } as Device

    render(<DeviceDetails device={camera} onClose={vi.fn()} onPropertyWrite={vi.fn()} onCommandExecute={vi.fn()} />)
    const preview = screen.getByLabelText('摄像头实时画面')
    fireEvent.playing(preview)
    fireEvent.waiting(preview)
    expect(screen.getByRole('status')).toHaveTextContent('正在追赶实时画面')
    act(() => vi.advanceTimersByTime(12_000))
    expect(screen.queryByText('等待关键帧超时')).not.toBeInTheDocument()
  })

  it('keeps camera media available while reporting a degraded Xiaomi control source', async () => {
    class StateSource { addEventListener() {} close() {} }
    vi.stubGlobal('EventSource', StateSource)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ data: [{
        key: { deviceId: 'camera-merged', endpointId: 'main', capabilityId: 'camera', propertyId: 'privacy' },
        value: null,
        providerId: 'xiaomi-hub',
        source: 'control-binding',
        observedAt: '2026-07-29T00:00:00Z',
        receivedAt: '2026-07-29T00:00:00Z',
        sequence: 1,
        version: 1,
        quality: 'unknown',
        known: false,
        available: false,
        unavailableReason: 'control-provider-offline',
      }] }),
    }))
    const camera: Device = {
      schemaVersion: 1, id: 'camera-merged', providerId: 'camera-main', name: '门口摄像头', type: 'camera',
      availability: 'online', online: true, lastUpdateAt: '2026-07-29T00:00:00Z',
      endpoints: [{ id: 'main', name: '主端点', type: 'camera', capabilities: [{
        id: 'camera', type: 'camera', properties: [{
          definition: { id: 'privacy', name: '隐私模式', type: 'bool', readable: true, writable: true, notifiable: true },
          value: { type: 'bool', bool: false },
        }],
      }] }],
    }

    render(<DeviceDetails device={camera} onClose={vi.fn()} onPropertyWrite={vi.fn()} onCommandExecute={vi.fn()} />)

    expect(screen.getByLabelText('摄像头实时画面')).toBeInTheDocument()
    expect(await screen.findByRole('status')).toHaveTextContent('视频可用，部分控制暂不可用')
    expect(screen.getByRole('status')).toHaveTextContent('xiaomi-hub')
    expect(screen.getByRole('button', { name: '设为 true' })).toBeDisabled()
  })

	it('renders unknown state as null semantics and disables writes', async () => {
		class StateSource { addEventListener() {} close() {} }
		vi.stubGlobal('EventSource', StateSource)
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [{ key: { deviceId: 'pending', endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, value: null, providerId: 'virtual-main', source: 'unknown', observedAt: '2026-07-13T00:00:00Z', receivedAt: '2026-07-13T00:00:00Z', sequence: 0, version: 1, quality: 'unknown', known: false, available: false, unavailableReason: 'availability-unknown' }] }) }))
		const device: Device = { schemaVersion: 1, id: 'pending', providerId: 'virtual-main', name: '待发现开关', type: 'switch', availability: 'unknown', online: false, lastUpdateAt: '2026-07-13T00:00:00Z', endpoints: [{ id: 'main', name: '主端点', type: 'switch', capabilities: [{ id: 'switch', type: 'switch', properties: [{ definition: { id: 'power', name: '开关', type: 'bool', parameterLevel: 'required', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: false } }] }] }] }
		render(<DeviceDetails device={device} onClose={vi.fn()} onPropertyWrite={vi.fn().mockResolvedValue(undefined)} onCommandExecute={vi.fn().mockResolvedValue(undefined)} />)
		expect(await screen.findByText('无历史值')).toBeInTheDocument()
		expect(screen.getByText(/availability-unknown/)).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '设为 true' })).toBeDisabled()
	})
  it('renders capability tree and state provenance', async () => {
    class StateSource { static current: StateSource; listeners = new Map<string, (event: { data: string }) => void>(); constructor() { StateSource.current = this } addEventListener(type: string, listener: (event: { data: string }) => void) { this.listeners.set(type, listener) } close() {} emit(type: string, value: unknown) { this.listeners.get(type)?.({ data: JSON.stringify(value) }) } }
    vi.stubGlobal('EventSource', StateSource)
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (path: string) => ({ ok: true, json: async () => path.endsWith('/states') ? ({ data: [{ key: { deviceId: 'switch-1', endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, value: { kind: 'bool', bool: true }, providerId: 'virtual-main', source: 'reported', observedAt: '2026-07-13T00:00:00Z', receivedAt: '2026-07-13T00:00:00Z', sequence: 7, version: 3, quality: 'reported' }] }) : ({ data: { definition: { id: 'power', name: '开关', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: false } } }) })))
    const device: Device = { schemaVersion: 1, id: 'switch-1', providerId: 'virtual-main', name: '客厅开关', type: 'switch', availability: 'online', online: true, lastUpdateAt: '2026-07-13T00:00:00Z', endpoints: [{ id: 'main', name: '主端点', type: 'switch', capabilities: [{ id: 'switch', type: 'switch', properties: [{ definition: { id: 'power', name: '开关', type: 'bool', parameterLevel: 'required', readable: true, writable: true, notifiable: true, staleAfterSeconds: 15 }, value: { type: 'bool', bool: true } }] }] }] }
    const onClose = vi.fn(); render(<DeviceDetails device={device} onClose={onClose} onPropertyWrite={vi.fn().mockResolvedValue(undefined)} onCommandExecute={vi.fn().mockResolvedValue(undefined)} />)
    expect(screen.getByText(/UNIFIED DEVICE MODEL.*switch/)).toBeInTheDocument(); expect(screen.getByText(/CAPABILITY/)).toBeInTheDocument(); expect(screen.getByText(/RWN/)).toBeInTheDocument(); expect(screen.getByText(/必需.*required/)).toBeInTheDocument(); expect(await screen.findByText('virtual-main · reported')).toBeInTheDocument(); expect(screen.getByText(/3.*seq.*7/)).toBeInTheDocument()
    act(() => StateSource.current.emit('state', { key: { deviceId: 'switch-1', endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, value: { kind: 'bool', bool: true }, providerId: 'virtual-main', source: 'reported', observedAt: '2026-07-13T00:00:00Z', receivedAt: '2026-07-13T00:00:02Z', sequence: 7, version: 4, quality: 'stale' })); expect(screen.getByText(/stale/)).toBeInTheDocument(); expect(screen.getByText(/4.*seq.*7/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /Provider.*读取/ })); expect(await screen.findByText('false')).toBeInTheDocument()
    await userEvent.keyboard('{Escape}'); expect(onClose).toHaveBeenCalledOnce()
  })
})
