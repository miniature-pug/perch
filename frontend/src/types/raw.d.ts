declare module "*?raw" {
  const content: string;
  export default content;
}

// Allow side-effect CSS imports, for example import "./tokens/tokens.css"
declare module "*.css" {
  const stylesheet: never;
  export default stylesheet;
}
