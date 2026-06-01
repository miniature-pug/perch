// perch-status.ts — opencode plugin reporting session status to perch.
// Auto-discovered from ~/.config/opencode/plugins/. Zero npm dependencies.
// Mirrors perch's internal/status.Machine: per-session dedup, a stale-trailing-
// busy gate (disarm on done, re-arm on a user message), waiting<->working on
// permission events. Shells `perch status set <state>` only on a real change.
export const PerchStatus = async ({ $ }: { $: any }) => {
  const sessions = new Map<string, { lastFired: string; acceptWorking: boolean }>()
  const get = (id: string) => {
    let s = sessions.get(id)
    if (!s) { s = { lastFired: "", acceptWorking: true }; sessions.set(id, s) }
    return s
  }
  const send = async (state: string) => {
    try { await $`perch status set ${state}`.quiet() } catch { /* never break the agent */ }
  }
  const fireWorking = async (id: string) => {
    const s = get(id)
    if (!s.acceptWorking) return
    if (s.lastFired === "working") return
    s.lastFired = "working"; await send("working")
  }
  const fireWorkingUnconditional = async (id: string) => {
    const s = get(id)
    if (s.lastFired === "working") return
    s.lastFired = "working"; await send("working")
  }
  const fireDone = async (id: string) => {
    const s = get(id)
    if (s.lastFired === "done") return
    s.lastFired = "done"; s.acceptWorking = false; await send("done")
  }
  const fireWaiting = async (id: string) => {
    const s = get(id)
    if (s.lastFired === "waiting") return
    s.lastFired = "waiting"; await send("waiting")
  }
  return {
    event: async ({ event }: { event: any }) => {
      const type: string = event?.type ?? ""
      const props: any = event?.properties ?? {}
      switch (type) {
        case "session.status": {
          const id = props.sessionID
          if (!id) return
          const st = props.status?.type
          if (st === "busy" || st === "retry") await fireWorking(id)
          else if (st === "idle") await fireDone(id)
          return
        }
        case "session.idle":
          if (props.sessionID) await fireDone(props.sessionID)
          return
        case "message.updated": {
          const info = props.info
          const sid = info?.sessionID ?? props.sessionID
          if (info?.role === "user" && sid) get(sid).acceptWorking = true
          return
        }
        case "permission.updated":
        case "permission.asked":
          if (props.sessionID) await fireWaiting(props.sessionID)
          return
        case "permission.replied":
          if (props.sessionID) await fireWorkingUnconditional(props.sessionID)
          return
      }
    },
  }
}
