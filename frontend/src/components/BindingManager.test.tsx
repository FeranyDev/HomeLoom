import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Device } from '../types/device'
import type { MappingBinding } from '../types/mapping'
import { BindingManager } from './BindingManager'
import { openProfileWorkbench } from '../profileDraft'
import '../styles.css'

vi.mock('../profileDraft', () => ({ openProfileWorkbench: vi.fn() }))

const device: Device = {
  schemaVersion: 1, id: 'virtual-switch-1', providerId: 'virtual-main', name: 'Virtual Switch', type: 'switch', availability: 'online', online: true, lastUpdateAt: new Date().toISOString(),
  endpoints: [{ id: 'main', name: 'Main', type: 'main', capabilities: [{ id: 'switch', type: 'switch', properties: [{ definition: { id: 'power', name: 'Power', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: false } }] }] }],
}
const sourceDevice = { ...device, catalog: { complete: true, source: 'miot-spec-cache', specType: 'urn:miot-spec-v2:device:switch:0000A003:vendor-v1:1', values: { 'main/switch/power': { known: true, available: true, observedAt: new Date().toISOString() } } } }

describe('BindingManager', () => {
  const catalog = vi.fn(async () => ({
    providers: [sourceDevice],
    models: [{ deviceType: 'switch' as const, version: 1, builtIn: true, custom: { publisher: { level: 'custom' as const, behavior: 'preserve' }, consumer: { level: 'custom' as const, behavior: 'explicit' } }, parameters: [{ path: { endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, name: '开关', level: 'required' as const, type: 'bool' as const, readable: true, writable: true, notifiable: true, publisher: { level: 'required' as const, behavior: 'must-publish' }, consumer: { level: 'required' as const, behavior: 'must-map' } }] }],
    consumers: [{ id: 'homekit', name: 'Apple Home / HomeKit', properties: [{ id: 'Switch.On', name: 'Switch.On', deviceType: 'switch' as const, defaultModelPath: { endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, level: 'required' as const, type: 'bool' as const, readable: true, writable: true, notifiable: true }] }],
  }))

  it('shows source, transformed, consumer, and effective numeric ranges while configuring', async () => {
    const light: Device = {
      ...device, id: 'monitor-light', name: '显示器挂灯', type: 'lightbulb',
      endpoints: [{ id: 'main', name: 'Main', type: 'main', capabilities: [{ id: 'light', type: 'light', properties: [{
        definition: { id: 'color-temperature', name: '色温', type: 'int', unit: 'mired', readable: true, writable: true, notifiable: true, min: 154, max: 370, step: 1 },
        value: { type: 'int', int: 250 },
      }] }] }],
    }
    const api = {
      listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []), create: vi.fn(), update: vi.fn(), remove: vi.fn(),
      catalog: vi.fn(async () => ({
        providers: [{ ...light, catalog: { complete: true, source: 'device-snapshot' } }],
        models: [{ deviceType: 'lightbulb' as const, version: 1, builtIn: true, custom: { publisher: { level: 'custom' as const, behavior: 'preserve' }, consumer: { level: 'custom' as const, behavior: 'explicit' } }, parameters: [{
          path: { endpointId: 'main', capabilityId: 'light', propertyId: 'color-temperature' }, name: '色温', level: 'optional' as const, type: 'int' as const, unit: 'mired', min: 50, max: 1000, step: 1,
          readable: true, writable: true, notifiable: true, publisher: { level: 'optional' as const, behavior: 'publish-if-supported' }, consumer: { level: 'optional' as const, behavior: 'map-if-supported' },
        }] }],
        consumers: [{ id: 'homekit', name: 'Apple Home / HomeKit', properties: [{
          id: 'Lightbulb.ColorTemperature', name: 'Lightbulb.ColorTemperature', deviceType: 'lightbulb' as const,
          defaultModelPath: { endpointId: 'main', capabilityId: 'light', propertyId: 'color-temperature' }, level: 'optional' as const, type: 'int' as const, unit: 'mired', min: 140, max: 500, step: 1,
          readable: true, writable: true, notifiable: true,
        }] }],
      })),
    }
    render(<BindingManager device={light} api={api} initialStage="consumer" consumerOnly consumerId="homekit" />)
    expect(await screen.findByText('数值范围（NUMERIC RANGE）')).toBeInTheDocument()
    expect(screen.getAllByText('50 ～ 1000，步长 1 mired')).toHaveLength(2)
    expect(screen.getAllByText('140 ～ 500，步长 1 mired')).toHaveLength(2)
    expect(screen.getByText('140 ～ 500，步长 1 mired', { selector: '.is-effective code' })).toBeInTheDocument()
  })

  it('allows property-card text to be selected for copying', async () => {
    render(<BindingManager device={device} api={{
      listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []), create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog,
    }} providerOnly />)

    const sourceProperty = await screen.findByRole('button', { name: /Virtual Switch.*Power/ })
    expect(getComputedStyle(sourceProperty).userSelect).toBe('text')
  })

  it('shows an effective identity default and saves a device-specific override', async () => {
    const create = vi.fn(async (input) => ({ ...input, id: 'default-override' }))
    const api = {
      listBindings: vi.fn(async () => []),
      listProfiles: vi.fn(async () => [{ schemaVersion: 1 as const, id: 'builtin-active-low', version: 1, kind: 'provider' as const, inputType: 'bool' as const, outputType: 'bool' as const, transforms: [{ type: 'invert' as const }], builtIn: true }]),
      create, update: vi.fn(), remove: vi.fn(), catalog,
    }

    render(<BindingManager device={device} api={api} providerOnly />)

    expect(await screen.findByText('模型默认（DEFAULT）')).toBeInTheDocument()
    expect(screen.getByText('1 / 1 生效')).toBeInTheDocument()
    expect(screen.getByText(/目标未被手工路由占用时生效/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '编辑默认映射 power' }))
    expect(screen.getByText(/正在修改默认映射/)).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('映射转换 Profile'), 'builtin-active-low')
    await userEvent.click(screen.getByRole('button', { name: '保存默认映射覆盖' }))

    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({
      stage: 'provider', providerId: 'virtual-main', deviceId: 'virtual-switch-1',
      endpointId: 'main', capabilityId: 'switch', propertyId: 'power',
      modelEndpointId: 'main', modelCapabilityId: 'switch', modelPropertyId: 'power',
      profileId: 'builtin-active-low', enabled: true,
    })))
  })

  it('puts required capabilities and their required properties first in the mapping editor', async () => {
    const baseModel = (await catalog()).models[0]
    const optionalStatus = {
      ...baseModel.parameters[0],
      path: { endpointId: 'main', capabilityId: 'status', propertyId: 'fault' },
      name: '故障',
      level: 'optional' as const,
      publisher: { level: 'optional' as const, behavior: 'publish-if-supported' },
      consumer: { level: 'optional' as const, behavior: 'map-if-supported' },
    }
    const optionalLock = {
      ...optionalStatus,
      path: { endpointId: 'main', capabilityId: 'switch', propertyId: 'lock' },
      name: '物理锁',
    }
    const requiredPower = baseModel.parameters[0]
    const api = {
      listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []),
      create: vi.fn(), update: vi.fn(), remove: vi.fn(),
      catalog: vi.fn(async () => ({ ...(await catalog()), models: [{ ...baseModel, parameters: [optionalStatus, optionalLock, requiredPower] }] })),
    }

    const { container } = render(<BindingManager device={device} api={api} providerOnly />)
    await screen.findByText('Virtual Switch · 来源属性映射')
    await waitFor(() => expect(container.querySelectorAll('.mapping-lane.is-model .mapping-node-list button')).toHaveLength(3))

    expect([...container.querySelectorAll<HTMLElement>('.mapping-lane.is-model .mapping-node-list button')].map((item) => item.textContent)).toEqual([
      expect.stringContaining('power'),
      expect.stringContaining('lock'),
      expect.stringContaining('fault'),
    ])
  })

  it('loads an existing database override back into the visual editor', async () => {
    const current: MappingBinding = { id: 'current-route', stage: 'provider', providerId: device.providerId, deviceId: device.id, deviceType: 'switch', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', modelEndpointId: 'main', modelCapabilityId: 'switch', modelPropertyId: 'power', enabled: true }
    const update = vi.fn(async (_id, input) => input)
    const api = {
      listBindings: vi.fn(async () => [current]),
      listProfiles: vi.fn(async () => [{ schemaVersion: 1 as const, id: 'builtin-active-low', version: 1, kind: 'provider' as const, inputType: 'bool' as const, outputType: 'bool' as const, transforms: [{ type: 'invert' as const }], builtIn: true }]),
      create: vi.fn(), update, remove: vi.fn(), catalog,
    }

    render(<BindingManager device={device} api={api} providerOnly />)

    await userEvent.click(await screen.findByRole('button', { name: '编辑映射路由 current-route' }))
    expect(screen.getByText(/正在编辑数据库路由 current-route/)).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('映射转换 Profile'), 'builtin-active-low')
    await userEvent.click(screen.getByRole('button', { name: '保存路由修改' }))
    await waitFor(() => expect(update).toHaveBeenCalledWith('current-route', expect.objectContaining({ id: 'current-route', profileId: 'builtin-active-low' })))
  })

  it('keeps a non-conflicting default while one source fans out through a manual route', async () => {
    const current: MappingBinding = { id: 'mirror-route', stage: 'provider', providerId: device.providerId, deviceId: device.id, deviceType: 'switch', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', modelEndpointId: 'main', modelCapabilityId: 'aux', modelPropertyId: 'mirrored-power', enabled: true }
    const fanOutCatalog = vi.fn(async () => ({
      ...(await catalog()),
      models: [{ ...(await catalog()).models[0], parameters: [
        ...(await catalog()).models[0].parameters,
        { path: { endpointId: 'main', capabilityId: 'aux', propertyId: 'mirrored-power' }, name: '镜像开关', level: 'custom' as const, type: 'bool' as const, readable: true, writable: true, notifiable: true, publisher: { level: 'custom' as const, behavior: 'explicit' }, consumer: { level: 'custom' as const, behavior: 'explicit' } },
      ] }],
    }))
    const api = { listBindings: vi.fn(async () => [current]), listProfiles: vi.fn(async () => []), create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog: fanOutCatalog }
    render(<BindingManager device={device} api={api} providerOnly />)
    expect(await screen.findByText('2 / 2 生效')).toBeInTheDocument()
    expect(screen.getByText('模型默认（DEFAULT）')).toBeInTheDocument()
    expect(screen.getByText(/同一来源可以保留多条/)).toBeInTheDocument()
    expect(screen.getByText(/main\.switch\.power → main\.aux\.mirrored-power/)).toBeInTheDocument()
  })

  it('creates a visual Provider to unified-model route', async () => {
    const create = vi.fn(async (input) => ({ ...input, id: 'binding-one' }))
    const api = {
      listBindings: vi.fn(async () => []),
		listProfiles: vi.fn(async () => [{ schemaVersion: 1 as const, id: 'builtin-active-low', version: 1, kind: 'provider' as const, inputType: 'bool' as const, outputType: 'bool' as const, transforms: [{ type: 'invert' as const }], builtIn: true }]),
      create, update: vi.fn(), remove: vi.fn(), catalog,
    }
    render(<BindingManager device={device} api={api} />)
    await screen.findByText('Virtual Switch的双段属性路由')
    expect(await screen.findByText('完整 · miot-spec-cache')).toBeInTheDocument()
    expect(screen.getByText('当前 false')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('option', { name: /builtin-active-low/ })).toBeInTheDocument())
    await userEvent.selectOptions(screen.getByLabelText('映射转换 Profile'), 'builtin-active-low')
    await userEvent.click(screen.getByRole('button', { name: /保存第.*一.*段路由/ }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ stage: 'provider', providerId: 'virtual-main', deviceId: 'virtual-switch-1', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', modelEndpointId: 'main', modelCapabilityId: 'switch', modelPropertyId: 'power', profileId: 'builtin-active-low', enabled: true })))
  })

  it('filters out incompatible and non-reversible profiles', async () => {
    const api = {
      listBindings: vi.fn(async () => []),
      listProfiles: vi.fn(async () => [
		{ schemaVersion: 1 as const, id: 'target-map', version: 1, kind: 'target' as const, inputType: 'bool' as const, outputType: 'bool' as const, transforms: [{ type: 'invert' as const }], builtIn: false },
		{ schemaVersion: 1 as const, id: 'number-map', version: 1, kind: 'provider' as const, inputType: 'number' as const, outputType: 'number' as const, transforms: [{ type: 'scale' as const, factor: 2 }], builtIn: false },
      ]),
      create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog,
    }
    render(<BindingManager device={device} api={api} />)
    await screen.findByText('Virtual Switch的双段属性路由')
    await waitFor(() => expect(screen.getByLabelText('映射转换 Profile')).toHaveValue(''))
    expect(screen.queryByRole('option', { name: /target-map/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /number-map/ })).not.toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /保存第.*一.*段路由/ })).toBeEnabled())
  })


  it('marks unified model parameters that already have a publisher binding during consumer mapping', async () => {
    const api = {
      listBindings: vi.fn(async () => [{
        id: 'provider-power', stage: 'provider' as const, enabled: true,
        providerId: 'virtual-main', deviceId: 'virtual-switch-1',
        endpointId: 'main', capabilityId: 'switch', propertyId: 'power',
        modelEndpointId: 'main', modelCapabilityId: 'switch', modelPropertyId: 'power',
      }]),
      listProfiles: vi.fn(async () => []),
      create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog,
    }
    render(<BindingManager device={device} api={api} initialStage="consumer" consumerOnly consumerId="homekit" targetId="apple-main" consumerDeviceId="living-switch" />)
    const mark = await screen.findByText('已绑定发布者')
    expect(mark).toBeInTheDocument()
    expect(mark.closest('button')?.className).toContain('is-publisher-bound')
    // Provider routes stay hidden in the consumer-only route list.
    expect(screen.queryByText('数据库覆盖（P → M）')).not.toBeInTheDocument()
  })

  it('creates Consumer routes for one concrete device', async () => {
    const create = vi.fn(async (input) => ({ ...input, id: 'consumer-one' }))
    const api = { listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []), create, update: vi.fn(), remove: vi.fn(), catalog }
    render(<BindingManager device={device} api={api} />)
    await screen.findByText('Virtual Switch的双段属性路由')
    await userEvent.click(screen.getByRole('button', { name: /② 统一模型.*Consumer/ }))
    await waitFor(() => expect(screen.getByLabelText('当前映射设备')).toHaveValue('Virtual Switch · virtual-main / virtual-switch-1'))
    expect(screen.queryByLabelText('Consumer 映射设备')).not.toBeInTheDocument()
    const save = await screen.findByRole('button', { name: /保存第.*二.*段路由/ })
    await waitFor(() => expect(save).toBeEnabled())
    await userEvent.click(save)
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({
      stage: 'consumer', providerId: 'virtual-main', deviceId: 'virtual-switch-1', deviceType: 'switch',
      consumerId: 'homekit', consumerProperty: 'Switch.On', modelEndpointId: 'main', modelCapabilityId: 'switch', modelPropertyId: 'power',
    })))
  })

  it('stores source and Consumer device types separately for cross-type binding', async () => {
    const create = vi.fn(async (input) => ({ ...input, id: 'consumer-cross-type' }))
    const crossTypeCatalog = vi.fn(async () => ({
      ...(await catalog()),
      consumers: [{ id: 'homekit', name: 'Apple Home / HomeKit', properties: [{ id: 'Outlet.On', name: 'Outlet.On', deviceType: 'outlet' as const, defaultModelPath: { endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, level: 'required' as const, type: 'bool' as const, readable: true, writable: true, notifiable: true }] }],
    }))
    const api = { listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []), create, update: vi.fn(), remove: vi.fn(), catalog: crossTypeCatalog }
    render(<BindingManager device={device} api={api} initialStage="consumer" consumerOnly consumerId="homekit" consumerDeviceType="outlet" targetId="apple-main" consumerDeviceId="living-outlet" />)
    await userEvent.click(await screen.findByRole('button', { name: /保存第.*二.*段路由/ }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({
      deviceType: 'switch', consumerDeviceType: 'outlet', consumerProperty: 'Outlet.On',
      modelEndpointId: 'main', modelCapabilityId: 'switch', modelPropertyId: 'power',
    })))
  })

  it('isolates Consumer routes for one bridge-owned virtual device', async () => {
    const current: MappingBinding = { id: 'bridge-current', stage: 'consumer', providerId: device.providerId, deviceId: device.id, deviceType: 'switch', modelEndpointId: 'main', modelCapabilityId: 'switch', modelPropertyId: 'power', targetId: 'apple-main', consumerDeviceId: 'living-switch', consumerId: 'homekit', consumerProperty: 'Switch.On', enabled: true }
    const other: MappingBinding = { ...current, id: 'bridge-other', targetId: 'apple-guest', consumerDeviceId: 'guest-switch' }
    const create = vi.fn(async (input) => ({ ...input, id: 'bridge-new' }))
    const api = { listBindings: vi.fn(async () => [current, other]), listProfiles: vi.fn(async () => []), create, update: vi.fn(), remove: vi.fn(), catalog }
    render(<BindingManager device={device} api={api} initialStage="consumer" consumerOnly consumerId="homekit" consumerLabel="客厅虚拟开关 · 属性映射" targetId="apple-main" consumerDeviceId="living-switch" />)
    await screen.findByText('客厅虚拟开关 · 属性映射')
    await waitFor(() => expect(screen.getByText('1 / 1 生效')).toBeInTheDocument())
    expect(screen.getByText('apple-main / living-switch')).toBeInTheDocument()
    expect(screen.queryByText('apple-guest / guest-switch')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /保存第.*二.*段路由/ }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({
      stage: 'consumer', providerId: 'virtual-main', deviceId: 'virtual-switch-1', targetId: 'apple-main', consumerDeviceId: 'living-switch', consumerId: 'homekit', consumerProperty: 'Switch.On',
    })))
  })

  it('uses only the Consumer catalog selected by the Target adapter', async () => {
    const create = vi.fn(async (input) => ({ ...input, id: 'matter-new' }))
    const currentHomeKit: MappingBinding = { id: 'homekit-route', stage: 'consumer', providerId: device.providerId, deviceId: device.id, deviceType: 'switch', modelEndpointId: 'main', modelCapabilityId: 'switch', modelPropertyId: 'power', targetId: 'matter-main', consumerDeviceId: 'matter-switch', consumerId: 'homekit', consumerProperty: 'Switch.On', enabled: true }
    const api = {
      listBindings: vi.fn(async () => [currentHomeKit]), listProfiles: vi.fn(async () => []), create, update: vi.fn(), remove: vi.fn(),
      catalog: vi.fn(async () => ({ ...(await catalog()), consumers: [
        ...(await catalog()).consumers,
        { id: 'matter', name: 'Matter', properties: [{ id: 'OnOff.OnOff', name: '开关状态', originalName: 'OnOff.OnOff', cluster: 'OnOff', element: 'OnOff', kind: 'attribute' as const, deviceType: 'switch' as const, defaultModelPath: { endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, level: 'required' as const, type: 'bool' as const, readable: true, writable: true, notifiable: true }, { id: 'OnOff.Toggle', name: '切换', originalName: 'OnOff.Toggle', cluster: 'OnOff', element: 'Toggle', kind: 'command' as const, deviceType: 'switch' as const, defaultModelPath: { endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, level: 'optional' as const, type: 'bool' as const, readable: false, writable: true, notifiable: false }] },
      ] })),
    }
    render(<BindingManager device={device} api={api} initialStage="consumer" consumerOnly consumerId="matter" targetId="matter-main" consumerDeviceId="matter-switch" />)
    expect(await screen.findByText('开关（OnOff）')).toBeInTheDocument()
    expect(screen.getByText('开关（OnOff） → 属性：开关状态（OnOff）')).toBeInTheDocument()
    expect(screen.getByText('开关（OnOff） → 命令：Toggle')).toBeInTheDocument()
    expect(screen.getByText('OnOff → Command → Toggle')).toBeInTheDocument()
    expect(screen.queryByText('Switch.On')).not.toBeInTheDocument()
    expect(screen.getByText('0 / 0 生效')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /保存第.*二.*段路由/ }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ consumerId: 'matter', consumerProperty: 'OnOff.OnOff' })))
  })

  it('hides other devices and their routes from the device editor', async () => {
    const otherDevice: Device = {
      ...device, id: 'virtual-switch-2', name: 'Other Switch',
      endpoints: [{ id: 'other-main', name: 'Other Main', type: 'main', capabilities: [{ id: 'switch', type: 'switch', properties: [{ definition: { id: 'other-power', name: 'Other Power', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: false } }] }] }],
    }
    const currentRoute: MappingBinding = { id: 'current-route', stage: 'provider', providerId: device.providerId, deviceId: device.id, deviceType: 'switch', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', modelEndpointId: 'main', modelCapabilityId: 'switch', modelPropertyId: 'power', enabled: true }
    const otherRoute: MappingBinding = { ...currentRoute, id: 'other-route', deviceId: otherDevice.id, endpointId: 'other-main', propertyId: 'other-power' }
    const api = {
      listBindings: vi.fn(async () => [currentRoute, otherRoute]), listProfiles: vi.fn(async () => []),
      create: vi.fn(), update: vi.fn(), remove: vi.fn(),
      catalog: vi.fn(async () => ({ ...(await catalog()), providers: [sourceDevice, { ...otherDevice, catalog: { complete: true, source: 'provider-discovery' } }] })),
    }

    render(<BindingManager device={device} api={api} />)
    await screen.findByText('Virtual Switch的双段属性路由')
    await waitFor(() => expect(screen.getByText('1 / 1 生效')).toBeInTheDocument())
    expect(screen.getByText(/Power · power/)).toBeInTheDocument()
    expect(screen.queryByText(/Other Power/)).not.toBeInTheDocument()
    expect(screen.queryByText(/other-main\.switch\.other-power/)).not.toBeInTheDocument()
  })

  it('does not present an unread initialization value as current', async () => {
    const unknownSource = { ...sourceDevice, catalog: { ...sourceDevice.catalog, values: { 'main/switch/power': { known: false, available: false, error: 'read timed out' } } } }
    const api = { listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []), create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog: vi.fn(async () => ({ ...(await catalog()), providers: [unknownSource] })) }
    render(<BindingManager device={device} api={api} />)
    await screen.findByText('当前值未知')
    expect(screen.queryByText('当前 false')).not.toBeInTheDocument()
    expect(screen.getByText('read timed out')).toBeInTheDocument()
  })

  it('visualizes format-equivalent and target-only enum values before saving', async () => {
	const airConditioner: Device = { schemaVersion: 1, id: 'xiaomi-126772242', providerId: 'xiaomi-2231ed', name: '空调伴侣', type: 'air-conditioner', availability: 'online', online: true, lastUpdateAt: new Date().toISOString(), endpoints: [] }
	const source = { ...airConditioner, endpoints: [{ id: 'miot-3', name: 'Fan Control', type: 'fan-control', capabilities: [{ id: 'service-3', type: 'fan-control', properties: [{ definition: { id: 'property-1', name: 'Fan Level', type: 'enum' as const, enum: ['Auto', 'Low', 'Medium', 'High'], readable: true, writable: true, notifiable: true }, value: { type: 'enum' as const, string: 'High' } }] }] }], catalog: { complete: true, source: 'miot-spec-cache' } }
	const enumCatalog = vi.fn(async () => ({ providers: [source], models: [{ deviceType: 'air-conditioner' as const, version: 2, builtIn: true, custom: { publisher: { level: 'custom' as const, behavior: 'preserve' }, consumer: { level: 'custom' as const, behavior: 'explicit' } }, parameters: [{ path: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'fan-speed' }, name: '风速档位', level: 'optional' as const, type: 'enum' as const, enum: ['auto', 'low', 'medium', 'high', 'turbo'], readable: true, writable: true, notifiable: true, publisher: { level: 'optional' as const, behavior: 'may-publish' }, consumer: { level: 'optional' as const, behavior: 'may-map' } }] }], consumers: [] }))
	const api = { listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []), create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog: enumCatalog }
	render(<BindingManager device={airConditioner} api={api} providerOnly />)
	const compatibility = await screen.findByRole('status')
	expect(within(compatibility).getByText('部分兼容，存在模型独有值')).toBeInTheDocument()
	expect(compatibility).toHaveTextContent('Auto → auto')
	expect(compatibility).toHaveTextContent('High → high')
	expect(compatibility).toHaveTextContent('模型独有：turbo')
	expect(screen.getByText(/来源枚举：Auto \/ Low \/ Medium \/ High/)).toBeInTheDocument()
	expect(screen.getByText(/模型枚举：auto \/ low \/ medium \/ high \/ turbo/)).toBeInTheDocument()
	expect(screen.getByRole('button', { name: /保存第.*一.*段路由/ })).toBeEnabled()
  })

  it('requires an enum Profile when source values cannot be aligned semantically', async () => {
	const airConditioner: Device = { schemaVersion: 1, id: 'xiaomi-ac', providerId: 'xiaomi-main', name: '空调', type: 'air-conditioner', availability: 'online', online: true, lastUpdateAt: new Date().toISOString(), endpoints: [] }
	const source = { ...airConditioner, endpoints: [{ id: 'miot-3', name: 'Fan', type: 'fan', capabilities: [{ id: 'service-3', type: 'fan', properties: [{ definition: { id: 'property-1', name: 'Fan Level', type: 'enum' as const, enum: ['Automatic', 'Silent'], readable: true, writable: true, notifiable: true }, value: { type: 'enum' as const, string: 'Silent' } }] }] }], catalog: { complete: true, source: 'miot-spec-cache' } }
	const enumCatalog = vi.fn(async () => ({ providers: [source], models: [{ deviceType: 'air-conditioner' as const, version: 2, builtIn: true, custom: { publisher: { level: 'custom' as const, behavior: 'preserve' }, consumer: { level: 'custom' as const, behavior: 'explicit' } }, parameters: [{ path: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'fan-speed' }, name: '风速档位', level: 'optional' as const, type: 'enum' as const, enum: ['auto', 'low', 'medium', 'high'], readable: true, writable: true, notifiable: true, publisher: { level: 'optional' as const, behavior: 'may-publish' }, consumer: { level: 'optional' as const, behavior: 'may-map' } }] }], consumers: [] }))
	const api = { listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => [{ schemaVersion: 1 as const, id: 'fan-level-map', version: 1, kind: 'provider' as const, inputType: 'enum' as const, outputType: 'enum' as const, transforms: [{ type: 'enum' as const, values: { Automatic: 'auto', Silent: 'low' } }], builtIn: false }]), create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog: enumCatalog }
	render(<BindingManager device={airConditioner} api={api} providerOnly />)
	const compatibility = await screen.findByRole('status')
	expect(within(compatibility).getByText('语义不一致，需要 Profile')).toBeInTheDocument()
	expect(compatibility).toHaveTextContent('无法自动对齐：Automatic / Silent')
	const save = screen.getByRole('button', { name: /保存第.*一.*段路由/ })
	expect(save).toBeDisabled()
	await userEvent.selectOptions(screen.getByLabelText('映射转换 Profile'), 'fan-level-map')
	expect(within(compatibility).getByText('由 Profile fan-level-map 转换')).toBeInTheDocument()
	expect(save).toBeEnabled()
  })

  it('shows consumer enum values while configuring the second-stage route', async () => {
    const airConditioner: Device = {
      schemaVersion: 1, id: 'virtual-ac-1', providerId: 'virtual-main', name: '虚拟空调', type: 'air-conditioner',
      availability: 'online', online: true, lastUpdateAt: new Date().toISOString(), endpoints: [],
    }
    const consumerEnumCatalog = vi.fn(async () => ({
      providers: [{ ...airConditioner, catalog: { complete: true, source: 'device-snapshot' } }],
      models: [{
        deviceType: 'air-conditioner' as const, version: 2, builtIn: true,
        custom: { publisher: { level: 'custom' as const, behavior: 'preserve' }, consumer: { level: 'custom' as const, behavior: 'explicit' } },
        parameters: [{
          path: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'fan-speed' },
          name: '风速档位', level: 'optional' as const, type: 'enum' as const, enum: ['auto', 'low', 'medium', 'high'],
          readable: true, writable: true, notifiable: true,
          publisher: { level: 'optional' as const, behavior: 'may-publish' },
          consumer: { level: 'optional' as const, behavior: 'may-map' },
        }],
      }],
      consumers: [{
        id: 'homekit', name: 'Apple Home / HomeKit', properties: [{
          id: 'HeaterCooler.RotationSpeed', name: 'HeaterCooler.RotationSpeed', deviceType: 'air-conditioner' as const,
          defaultModelPath: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'fan-speed' },
          level: 'optional' as const, type: 'enum' as const, enum: ['auto', 'low', 'medium', 'high', 'turbo'],
          readable: true, writable: true, notifiable: true,
        }],
      }],
    }))
    const api = {
      listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []),
      create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog: consumerEnumCatalog,
    }
    render(<BindingManager device={airConditioner} api={api} initialStage="consumer" consumerOnly consumerId="homekit" />)
    expect(await screen.findByText(/模型枚举：auto \/ low \/ medium \/ high/)).toBeInTheDocument()
    expect(screen.getByText(/消费端枚举：auto \/ low \/ medium \/ high \/ turbo/)).toBeInTheDocument()
    const compatibility = await screen.findByRole('status')
    expect(within(compatibility).getByText('部分兼容，存在消费端独有值')).toBeInTheDocument()
    expect(compatibility).toHaveTextContent('auto → auto')
    expect(compatibility).toHaveTextContent('high → high')
    expect(compatibility).toHaveTextContent('消费端独有：turbo')
    expect(within(compatibility).getByText('统一模型值域（Model）')).toBeInTheDocument()
    expect(within(compatibility).getByText('消费端值域（Consumer）')).toBeInTheDocument()
  })

  it('requires an enum Profile when consumer values cannot be aligned from the model', async () => {
    const airConditioner: Device = {
      schemaVersion: 1, id: 'virtual-ac-2', providerId: 'virtual-main', name: '虚拟空调', type: 'air-conditioner',
      availability: 'online', online: true, lastUpdateAt: new Date().toISOString(), endpoints: [],
    }
    const consumerEnumCatalog = vi.fn(async () => ({
      providers: [{ ...airConditioner, catalog: { complete: true, source: 'device-snapshot' } }],
      models: [{
        deviceType: 'air-conditioner' as const, version: 2, builtIn: true,
        custom: { publisher: { level: 'custom' as const, behavior: 'preserve' }, consumer: { level: 'custom' as const, behavior: 'explicit' } },
        parameters: [{
          path: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'fan-speed' },
          name: '风速档位', level: 'optional' as const, type: 'enum' as const, enum: ['Automatic', 'Silent'],
          readable: true, writable: true, notifiable: true,
          publisher: { level: 'optional' as const, behavior: 'may-publish' },
          consumer: { level: 'optional' as const, behavior: 'may-map' },
        }],
      }],
      consumers: [{
        id: 'homekit', name: 'Apple Home / HomeKit', properties: [{
          id: 'HeaterCooler.RotationSpeed', name: 'HeaterCooler.RotationSpeed', deviceType: 'air-conditioner' as const,
          defaultModelPath: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'fan-speed' },
          level: 'optional' as const, type: 'enum' as const, enum: ['auto', 'low', 'medium', 'high'],
          readable: true, writable: true, notifiable: true,
        }],
      }],
    }))
    const api = {
      listBindings: vi.fn(async () => []),
      listProfiles: vi.fn(async () => [{
        schemaVersion: 1 as const, id: 'model-to-homekit-fan', version: 1, kind: 'target' as const,
        inputType: 'enum' as const, outputType: 'enum' as const,
        transforms: [{ type: 'enum' as const, values: { Automatic: 'auto', Silent: 'low' } }], builtIn: false,
      }]),
      create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog: consumerEnumCatalog,
    }
    render(<BindingManager device={airConditioner} api={api} initialStage="consumer" consumerOnly consumerId="homekit" />)
    const compatibility = await screen.findByRole('status')
    expect(within(compatibility).getByText('语义不一致，需要 Profile')).toBeInTheDocument()
    expect(compatibility).toHaveTextContent('无法自动对齐：Automatic / Silent')
    const save = screen.getByRole('button', { name: /保存第.*二.*段路由/ })
    expect(save).toBeDisabled()
    await userEvent.selectOptions(screen.getByLabelText('映射转换 Profile'), 'model-to-homekit-fan')
    expect(within(compatibility).getByText('由 Profile model-to-homekit-fan 转换')).toBeInTheDocument()
    expect(save).toBeEnabled()
  })

  it('keeps consumer properties and enum panel unobstructed during second-stage mapping', async () => {
    const airConditioner: Device = {
      schemaVersion: 1, id: 'virtual-ac-layout', providerId: 'virtual-main', name: '虚拟空调', type: 'air-conditioner',
      availability: 'online', online: true, lastUpdateAt: new Date().toISOString(), endpoints: [],
    }
    const catalog = vi.fn(async () => ({
      providers: [{ ...airConditioner, catalog: { complete: true, source: 'device-snapshot' } }],
      models: [{
        deviceType: 'air-conditioner' as const, version: 2, builtIn: true,
        custom: { publisher: { level: 'custom' as const, behavior: 'preserve' }, consumer: { level: 'custom' as const, behavior: 'explicit' } },
        parameters: [{
          path: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'fan-speed' },
          name: '风速档位', level: 'optional' as const, type: 'enum' as const, enum: ['auto', 'low', 'medium', 'high'],
          readable: true, writable: true, notifiable: true,
          publisher: { level: 'optional' as const, behavior: 'may-publish' },
          consumer: { level: 'optional' as const, behavior: 'may-map' },
        }],
      }],
      consumers: [{
        id: 'homekit', name: 'Apple Home / HomeKit', properties: [
          { id: 'HeaterCooler.Active', name: 'HeaterCooler.Active', deviceType: 'air-conditioner' as const, defaultModelPath: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'power' }, level: 'required' as const, type: 'bool' as const, readable: true, writable: true, notifiable: true },
          { id: 'HeaterCooler.RotationSpeed', name: 'HeaterCooler.RotationSpeed', deviceType: 'air-conditioner' as const, defaultModelPath: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'fan-speed' }, level: 'optional' as const, type: 'enum' as const, enum: ['auto', 'low', 'medium', 'high', 'turbo'], readable: true, writable: true, notifiable: true },
          { id: 'HeaterCooler.CurrentTemperature', name: 'HeaterCooler.CurrentTemperature', deviceType: 'air-conditioner' as const, defaultModelPath: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'temperature' }, level: 'optional' as const, type: 'number' as const, readable: true, writable: false, notifiable: true },
        ],
      }],
    }))
    const api = {
      listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []),
      create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog,
    }
    const { container } = render(<BindingManager device={airConditioner} api={api} initialStage="consumer" consumerOnly consumerId="homekit" />)
    await screen.findByText('消费端（CONSUMERS）')
    const consumerLanes = [...container.querySelectorAll('.mapping-lane')].filter((lane) => !lane.classList.contains('is-model') && !lane.classList.contains('is-context'))
    expect(consumerLanes).toHaveLength(1)
    const consumerList = consumerLanes[0].querySelector('.mapping-node-list') as HTMLElement
    expect(consumerList).not.toBeNull()
    await waitFor(() => expect(consumerList.querySelectorAll('button').length).toBeGreaterThanOrEqual(3))
    // Last consumer property remains in the scrollable list, not clipped out of the lane.
    expect(within(consumerList).getByText('HeaterCooler.CurrentTemperature')).toBeInTheDocument()
    await userEvent.click(within(consumerList).getByRole('button', { name: /HeaterCooler\.RotationSpeed/ }))
    expect(await screen.findByText(/消费端枚举：auto \/ low \/ medium \/ high \/ turbo/)).toBeInTheDocument()
    const compatibility = await screen.findByRole('status')
    expect(compatibility).toHaveClass('enum-compatibility')
    expect(compatibility.parentElement).toHaveClass('mapping-route-toolbar')
    // Enum panel must stay a block/grid sibling under the toolbar, not collapse into the action flex row.
    expect(container.querySelector('.mapping-route-actions')).not.toBeNull()
    expect(container.querySelector('.mapping-route-actions .enum-compatibility')).toBeNull()
    expect(compatibility.compareDocumentPosition(consumerList) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy()
  })

  it('offers a quick jump to the profile workbench when enum domains require conversion', async () => {
    const airConditioner: Device = { schemaVersion: 1, id: 'xiaomi-ac', providerId: 'xiaomi-main', name: '空调', type: 'air-conditioner', availability: 'online', online: true, lastUpdateAt: new Date().toISOString(), endpoints: [] }
    const source = { ...airConditioner, endpoints: [{ id: 'miot-3', name: 'Fan', type: 'fan', capabilities: [{ id: 'service-3', type: 'fan', properties: [{ definition: { id: 'property-1', name: 'Fan Level', type: 'enum' as const, enum: ['Automatic', 'Silent'], readable: true, writable: true, notifiable: true }, value: { type: 'enum' as const, string: 'Silent' } }] }] }], catalog: { complete: true, source: 'miot-spec-cache' } }
    const enumCatalog = vi.fn(async () => ({ providers: [source], models: [{ deviceType: 'air-conditioner' as const, version: 2, builtIn: true, custom: { publisher: { level: 'custom' as const, behavior: 'preserve' }, consumer: { level: 'custom' as const, behavior: 'explicit' } }, parameters: [{ path: { endpointId: 'main', capabilityId: 'air-conditioner', propertyId: 'fan-speed' }, name: '风速档位', level: 'optional' as const, type: 'enum' as const, enum: ['auto', 'low', 'medium', 'high'], readable: true, writable: true, notifiable: true, publisher: { level: 'optional' as const, behavior: 'may-publish' }, consumer: { level: 'optional' as const, behavior: 'may-map' } }] }], consumers: [] }))
    const api = { listBindings: vi.fn(async () => []), listProfiles: vi.fn(async () => []), create: vi.fn(), update: vi.fn(), remove: vi.fn(), catalog: enumCatalog }
    render(<BindingManager device={airConditioner} api={api} providerOnly />)
    await screen.findByRole('status')
    await userEvent.click(screen.getByRole('button', { name: '去配置转换 Profile' }))
    expect(openProfileWorkbench).toHaveBeenCalledWith(expect.objectContaining({
      stage: 'provider', inputType: 'enum', outputType: 'enum',
      sourceEnum: ['Automatic', 'Silent'], targetEnum: ['auto', 'low', 'medium', 'high'],
    }))
  })

  it('refreshes the profile select list without leaving the mapping editor', async () => {
    const api = {
      listBindings: vi.fn(async () => []),
      listProfiles: vi.fn()
        .mockResolvedValueOnce([])
        .mockResolvedValueOnce([{ schemaVersion: 1 as const, id: 'fresh-enum-map', version: 1, kind: 'provider' as const, inputType: 'bool' as const, outputType: 'bool' as const, transforms: [{ type: 'invert' as const }], builtIn: false }]),
      create: vi.fn(), update: vi.fn(), remove: vi.fn(),
      catalog,
    }
    render(<BindingManager device={device} api={api} providerOnly />)
    await screen.findByLabelText('映射转换 Profile')
    expect(screen.queryByRole('option', { name: /fresh-enum-map/ })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '刷新转换配置列表' }))
    expect(await screen.findByRole('option', { name: /fresh-enum-map/ })).toBeInTheDocument()
    expect(api.listProfiles).toHaveBeenCalledTimes(2)
  })
})
