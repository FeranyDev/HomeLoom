import { useState } from 'react'
import { MappingPreview } from './MappingPreview'
import { ProfileManager } from './ProfileManager'
import { BindingManager } from './BindingManager'
import type { Device } from '../types/device'

export function MappingWorkspace({ devices }: { devices: Device[] }) {
  const [profileRevision, setProfileRevision] = useState(0)
  return <section className="mapping-page"><ProfileManager onChanged={() => setProfileRevision((current) => current + 1)} /><BindingManager devices={devices} profileRevision={profileRevision} /><MappingPreview profileRevision={profileRevision} /></section>
}
