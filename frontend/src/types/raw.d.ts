declare module "*?raw" {
  const content: string;
  export default content;
}

// Allow side-effect CSS imports (e.g. import "./tokens/tokens.css")
declare module "*.css" {
  const stylesheet: never;
  export default stylesheet;
}
