import { FeedbackState } from '../../../components/feedback-state'
import { Skeleton } from '../../../components/ui/skeleton'
import type { IntentRun } from '../../../lib/api/contracts'
import { DecisionConclusion } from './decision-conclusion'
import { DeliveryPanel } from './delivery-panel'
import { FlowCanvas } from './flow-canvas'
import { ReplayControls } from './replay-controls'
import { useIntentFlow } from './queries'

/** Complete read-only view of one run: conclusion, flow nodes and delivery. */
export function FlowRunView({
  run,
  compact = false,
  showDelivery = true,
}: {
  run?: IntentRun
  compact?: boolean
  showDelivery?: boolean
}) {
  const flow = useIntentFlow()
  if (flow.isError) {
    return <FeedbackState variant="error" error={flow.error} onRetry={() => void flow.refetch()} />
  }
  if (flow.isLoading || !flow.data) {
    return <Skeleton className="h-64 w-full" />
  }
  const showSnapshotStatus = Boolean(run && run.decisionId > 0)
  return (
    <div className="space-y-4">
      <DecisionConclusion run={run} showSnapshotStatus={showSnapshotStatus} />
      <FlowCanvas flow={flow.data} run={run} compact={compact} showSnapshotStatus={showSnapshotStatus} />
      {showDelivery ? <DeliveryPanel run={run} /> : null}
      {run && run.decisionId > 0 ? <ReplayControls decisionId={run.decisionId} /> : null}
    </div>
  )
}
