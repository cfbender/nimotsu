import { useState } from 'react'
import { type EmailCandidate } from './api'
import { MailIcon } from './icons'

export function CandidateList({
  candidates,
  onTrack,
  onDismiss,
}: {
  candidates: EmailCandidate[]
  onTrack: (candidate: EmailCandidate) => Promise<void>
  onDismiss: (candidate: EmailCandidate) => Promise<void>
}) {
  const [workingID, setWorkingID] = useState('')

  async function run(candidate: EmailCandidate, action: (candidate: EmailCandidate) => Promise<void>) {
    setWorkingID(candidate.id)
    try {
      await action(candidate)
    } finally {
      setWorkingID('')
    }
  }

  return (
    <section className="candidate-section" aria-labelledby="candidate-heading">
      <div className="section-heading">
        <div>
          <p className="eyebrow">From Gmail</p>
          <h2 id="candidate-heading">Ready to review</h2>
        </div>
        <span>{candidates.length}</span>
      </div>
      <div className="candidate-list">
        {candidates.map((candidate) => (
          <article className="candidate-card" key={candidate.id}>
            <div className="candidate-icon"><MailIcon /></div>
            <div className="candidate-copy">
              <h3>{candidate.description}</h3>
              <p>
                <span>{candidate.sender || 'Gmail'}</span>
                <span aria-hidden="true">·</span>
                <time dateTime={candidate.message_at}>{new Date(candidate.message_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</time>
              </p>
              <code>{candidate.tracking_number}</code>
            </div>
            <div className="candidate-actions">
              <button className="primary-button" disabled={workingID === candidate.id} type="button" onClick={() => void run(candidate, onTrack)}>Track</button>
              <button disabled={workingID === candidate.id} type="button" onClick={() => void run(candidate, onDismiss)}>Dismiss</button>
            </div>
          </article>
        ))}
      </div>
    </section>
  )
}
