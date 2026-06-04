/** Focus the node immediately on mount (keyboard a11y for dialogs; avoids the autofocus lint warning). */
export function focusOnMount(node: HTMLElement) { node.focus(); }
