import { render, screen, fireEvent } from "@testing-library/svelte";

test("renders tabs and invokes onclose", async () => {
  const { default: Tabs } = await import("./Tabs.svelte");
  let closed = "";
  render(Tabs, {
    props: { tabs: [{ id: "t1", label: "feat-x" }], activeId: "t1", onclose: (id: string) => (closed = id) },
  });
  await fireEvent.click(screen.getByRole("button", { name: /close feat-x/i }));
  expect(closed).toBe("t1");
});

test("invokes onselect when a tab is clicked", async () => {
  const { default: Tabs } = await import("./Tabs.svelte");
  let sel = "";
  render(Tabs, {
    props: { tabs: [{ id: "t1", label: "feat-x" }], activeId: "t1", onselect: (id: string) => (sel = id) },
  });
  await fireEvent.click(screen.getByRole("tab", { name: "feat-x" }));
  expect(sel).toBe("t1");
});
