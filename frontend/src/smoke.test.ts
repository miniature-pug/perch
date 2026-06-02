import { render, screen } from "@testing-library/svelte";
import App from "./App.svelte";

test("renders the perch title", () => {
  render(App);
  expect(screen.getByRole("heading", { name: "perch" })).toBeInTheDocument();
});
