import { FeedbackState } from '../../../components/feedback-state'
import { Skeleton } from '../../../components/ui/skeleton'
import type { IntentRun } from '../../../lib/api/contracts'
import { CooldownPlayback } from './cooldown-playback'
import { DecisionConclusion } from './decision-conclusion'
import { DeliveryPanel } from './delivery-panel'
import { FlowCanvas } from './flow-canvas'
import { ReplayControls } from './replay-controls'
import { useIntentFlow } from './queries'

/** Complete read-only view of one run: conclusion, flow nodes and delivery. */
export function FlowRunView({
  run,
  recording,
  compact = false,
  showDelivery = true,
  showReplay = true,
}: {
  run?: IntentRun
  recording?: Pick<IntentRun, 'nodes' | 'edges'>
  compact?: boolean
  showDelivery?: boolean
  showReplay?: boolean
}) {
  const flow = useIntentFlow()
  if (flow.isError) {
    return <FeedbackState variant="error" error={flow.error} onRetry={() => void flow.refetch()} />
  }
  if (flow.isLoading || !flow.data) {
    return <Skeleton className="h-64 w-full" />
  }
  const showSnapshotStatus = Boolean(!recording && run && run.decisionId > 0)
  return (
    <div className="space-y-4">
      {(run || !recording) && <DecisionConclusion run={run} showSnapshotStatus={showSnapshotStatus} />}
      <FlowCanvas flow={flow.data} run={run} recording={recording} compact={compact} showSnapshotStatus={showSnapshotStatus} />
      {run && run.decisionId > 0 ? <CooldownPlayback run={run} /> : null}
      {showDelivery ? <DeliveryPanel run={run} /> : null}
      {showReplay && run && run.decisionId > 0 ? <ReplayControls decisionId={run.decisionId} /> : null}
    </div>
  )
}
