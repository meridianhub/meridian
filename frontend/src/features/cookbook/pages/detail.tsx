import { useParams } from 'react-router-dom'

export function CookbookDetailPage() {
  const { name } = useParams<{ name: string }>()
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">{name}</h1>
      <p className="text-muted-foreground">Pattern detail placeholder</p>
    </div>
  )
}
