import "./reset.css";
import "./tokens/tokens.css";
import "./tokens/themes.css";
import "./tokens/glass.css";
import "@xterm/xterm/css/xterm.css";
import { mount } from "svelte";
import Root from "./Root.svelte";

const app = mount(Root, { target: document.getElementById("app")! });
export default app;
