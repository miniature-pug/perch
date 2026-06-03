// frontend/src/lib/TokenMeter.test.ts
import { render, screen } from "@testing-library/svelte";

test("renders formatted token count and cost (large number abbreviation)", async () => {
  const { default: TokenMeter } = await import("./TokenMeter.svelte");
  render(TokenMeter, { props: { tokens: 12500, cost: 0.043, capsTokens: true } });
  expect(screen.getByRole("status")).toBeInTheDocument();
  // 12500 → "12.5k"
  expect(screen.getByText(/12\.5k tok/)).toBeInTheDocument();
  expect(screen.getByText(/\$0\.04/)).toBeInTheDocument();
});

test("renders with small token count unabbreviated", async () => {
  const { default: TokenMeter } = await import("./TokenMeter.svelte");
  render(TokenMeter, { props: { tokens: 999, cost: 0.1, capsTokens: false } });
  expect(screen.getByRole("status")).toBeInTheDocument();
  expect(screen.getByText(/999 tok/)).toBeInTheDocument();
});

test("abbreviates exactly 1000 as 1k (no trailing .0)", async () => {
  const { default: TokenMeter } = await import("./TokenMeter.svelte");
  render(TokenMeter, { props: { tokens: 1000, cost: 0.0, capsTokens: false } });
  expect(screen.getByText(/^1k tok$/)).toBeInTheDocument();
});

test("abbreviates 1200000 as 1.2M", async () => {
  const { default: TokenMeter } = await import("./TokenMeter.svelte");
  render(TokenMeter, { props: { tokens: 1_200_000, cost: 1.5, capsTokens: false } });
  expect(screen.getByText(/^1\.2M tok$/)).toBeInTheDocument();
});

test("meter is always in dim color — never has at-cap class", async () => {
  const { default: TokenMeter } = await import("./TokenMeter.svelte");
  render(TokenMeter, { props: { tokens: 0, cost: 0, capsTokens: true } });
  const el = screen.getByRole("status");
  expect(el.classList.contains("at-cap")).toBe(false);
});
