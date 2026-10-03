export type Mode = "normal" | "terminal" | "command";

class ModeStore {
  current = $state<Mode>("normal");
  /** True after Ctrl-\ in TERMINAL mode, until Ctrl-n completes the leave
      sequence or another key cancels it. Shared by the window keymap and the
      xterm key handler (see lib/terminalKeys.ts). */
  leavePending = false;

  /** `i` in normal mode. All keys pass to the pty. */
  enterTerminal(): void  { this.leavePending = false; this.current = "terminal"; }
  /** `Ctrl-\ Ctrl-n`, or a click on the chrome. */
  leaveTerminal(): void  { this.leavePending = false; if (this.current === "terminal") this.current = "normal"; }
  /** `:` or `Ctrl-K`. */
  enterCommand(): void   { this.current = "command"; }
  /** `Esc`, or running the command. */
  leaveCommand(): void   { if (this.current === "command")  this.current = "normal"; }
}

export const mode = new ModeStore();
