import { useState } from 'react'
import { MappingPreview } from './MappingPreview'
import { ProfileManager } from './ProfileManager'

export function MappingWorkspace() {
  const [profileRevision, setProfileRevision] = useState(0)
  return <section className="mapping-page"><ProfileManager onChanged={() => setProfileRevision((current) => current + 1)} /><MappingPreview profileRevision={profileRevision} /></section>
}
