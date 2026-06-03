export type Mode = "normal" | "terminal" | "command";

class ModeStore {
  current = $state<Mode>("normal");

  /** `i` in NORMAL — all keys pass to pty. */
  enterTerminal(): void  { this.current = "terminal"; }
  /** `Ctrl-\ Ctrl-n` or click chrome. */
  leaveTerminal(): void  { if (this.current === "terminal") this.current = "normal"; }
  /** `:` or `Ctrl-K`. */
  enterCommand(): void   { this.current = "command"; }
  /** `Esc` or run. */
  leaveCommand(): void   { if (this.current === "command")  this.current = "normal"; }
}

export const mode = new ModeStore();
