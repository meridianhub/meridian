import { useEffect, useState } from 'react'

interface VersionInfo {
  version: string
  commit: string
  build_date: string
}

export function BuildInfo() {
  const [info, setInfo] = useState<VersionInfo | null>(null)

  useEffect(() => {
    fetch('/version')
      .then((res) => (res.ok ? res.json() : Promise.reject(res)))
      .then((data: VersionInfo) => setInfo(data))
      .catch(() => {
        // Fall back to build-time env vars
        const version = import.meta.env.VITE_BUILD_VERSION
        const commit = import.meta.env.VITE_BUILD_COMMIT
        if (version || commit) {
          setInfo({
            version: version ?? 'dev',
            commit: commit ?? 'unknown',
            build_date: 'unknown',
          })
        }
      })
  }, [])

  if (!info) return null

  const shortCommit =
    info.commit && info.commit !== 'unknown' ? info.commit.slice(0, 7) : null
  const label = shortCommit ? `${info.version} (${shortCommit})` : info.version

  return (
    <div className="px-3 py-2 text-xs text-muted-foreground">
      <span title={info.commit}>{label}</span>
    </div>
  )
}
