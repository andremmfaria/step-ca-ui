import { useEffect, useState } from 'react'
import { getSession } from '@andremmfaria/step-ca-ui-client'
import { client } from '@andremmfaria/step-ca-ui-client/client'

// baseUrl is empty because nginx (Vite in dev) serves the document and proxies
// the API on one origin, so every request is same-origin and relative.
client.setConfig({ baseUrl: '', credentials: 'same-origin' })

type Session = Awaited<ReturnType<typeof getSession>>['data']

export function App() {
  const [session, setSession] = useState<Session>()
  const [error, setError] = useState<string>()

  useEffect(() => {
    getSession()
      .then((res) => setSession(res.data))
      .catch((e: unknown) => setError(String(e)))
  }, [])

  if (error) return <p data-testid="error">unreachable: {error}</p>
  if (!session) return <p data-testid="loading">loading</p>

  return (
    <main>
      <h1>step-ca-ui spike</h1>
      <p data-testid="state">state: {session.state}</p>
      {session.user ? <p data-testid="username">user: {session.user.username}</p> : null}
    </main>
  )
}
