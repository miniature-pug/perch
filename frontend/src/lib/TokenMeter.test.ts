// frontend/src/lib/TokenMeter.test.ts
import { render, screen } from "@testing-library/svelte";

test("renders token count and cost when capsTokens is true", async () => {
  const { default: TokenMeter } = await import("./TokenMeter.svelte");
  render(TokenMeter, { props: { tokens: 12500, cost: 0.043, capsTokens: true } });
  expect(screen.getByRole("status")).toBeInTheDocument();
  expect(screen.getByText(/12,500/)).toBeInTheDocument();
  expect(screen.getByText(/\$0\.04/)).toBeInTheDocument();
});

test("renders nothing when capsTokens is false", async () => {
  const { default: TokenMeter } = await import("./TokenMeter.svelte");
  render(TokenMeter, { props: { tokens: 999, cost: 0.1, capsTokens: false } });
  expect(screen.queryByRole("status")).toBeNull();
});
