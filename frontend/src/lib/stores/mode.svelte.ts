export type Mode = "normal" | "terminal" | "command";

class ModeStore {
  current = $state<Mode>("normal");

  /** `i` in normal mode. All keys pass to the pty. */
  enterTerminal(): void  { this.current = "terminal"; }
  /** `Ctrl-\ Ctrl-n`, or a click on the chrome. */
  leaveTerminal(): void  { if (this.current === "terminal") this.current = "normal"; }
  /** `:` or `Ctrl-K`. */
  enterCommand(): void   { this.current = "command"; }
  /** `Esc`, or running the command. */
  leaveCommand(): void   { if (this.current === "command")  this.current = "normal"; }
}

export const mode = new ModeStore();
