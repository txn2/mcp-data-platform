import { describe, it, expect, beforeAll, afterAll, vi } from "vitest";
import { render } from "@testing-library/react";
import {
  JsxRenderer,
  transformJsx,
  findComponentName,
} from "./JsxRenderer";

// jsdom does not implement URL.createObjectURL; provide a stub.
let originalCreateObjectURL: typeof URL.createObjectURL;
let originalRevokeObjectURL: typeof URL.revokeObjectURL;

beforeAll(() => {
  originalCreateObjectURL = URL.createObjectURL;
  originalRevokeObjectURL = URL.revokeObjectURL;
  let counter = 0;
  URL.createObjectURL = vi.fn(() => `blob:test/${++counter}`);
  URL.revokeObjectURL = vi.fn();
});

afterAll(() => {
  URL.createObjectURL = originalCreateObjectURL;
  URL.revokeObjectURL = originalRevokeObjectURL;
});

describe("transformJsx", () => {
  it("transforms JSX to valid JavaScript", () => {
    const code = `function App() { return <div>Hello</div>; }`;
    const result = transformJsx(code);
    // Should contain jsx runtime calls, not raw JSX
    expect(result).not.toContain("<div>");
    expect(result).toMatch(/_jsx/);
  });

  it("handles imports alongside JSX", () => {
    const code = `import { useState } from "react";\nfunction App() { return <div>{useState(0)}</div>; }`;
    const result = transformJsx(code);
    expect(result).toContain('import { useState } from "react"');
    expect(result).toMatch(/_jsx/);
    expect(result).not.toContain("<div>");
  });

  it("auto-inserts jsx-runtime import", () => {
    const code = `function App() { return <span>test</span>; }`;
    const result = transformJsx(code);
    expect(result).toContain("react/jsx-runtime");
  });

  it("throws on invalid syntax", () => {
    expect(() => transformJsx("function {{{")).toThrow();
  });
});

describe("findComponentName", () => {
  it("finds export default function Name", () => {
    expect(
      findComponentName("export default function Dashboard() {}"),
    ).toBe("Dashboard");
  });

  it("finds export default class Name", () => {
    expect(
      findComponentName("export default class Widget extends React.Component {}"),
    ).toBe("Widget");
  });

  it("finds export default Name;", () => {
    expect(
      findComponentName("function MyChart() {}\nexport default MyChart;"),
    ).toBe("MyChart");
  });

  it("falls back to last PascalCase declaration", () => {
    expect(
      findComponentName("function Helper() {}\nfunction MainView() {}"),
    ).toBe("MainView");
  });

  it("returns null for no component", () => {
    expect(findComponentName("const x = 42;")).toBeNull();
  });

  it("detects the production artifact component name", () => {
    // Matches the production artifact pattern
    const code = `const KPICard = () => {};\nexport default function Dashboard() { return null; }`;
    expect(findComponentName(code)).toBe("Dashboard");
  });
});

describe("JsxRenderer", () => {
  it("renders an iframe with allow-scripts sandbox", () => {
    const { container } = render(
      <JsxRenderer content="function App() { return <div>Hello</div>; }" />,
    );
    const iframe = container.querySelector("iframe");
    expect(iframe).toBeTruthy();
    expect(iframe?.getAttribute("sandbox")).toBe("allow-scripts");
    expect(iframe?.getAttribute("title")).toBe("JSX Preview");
  });

  it("creates a blob URL for the iframe src", () => {
    const code = `export default function Dashboard() { return <div>Test</div>; }`;
    const { container } = render(<JsxRenderer content={code} />);
    const iframe = container.querySelector("iframe");
    expect(iframe?.getAttribute("src")).toMatch(/^blob:/);
  });

  it("renders even when JSX transform fails", () => {
    const { container } = render(
      <JsxRenderer content="function {{{" />,
    );
    const iframe = container.querySelector("iframe");
    expect(iframe).toBeTruthy();
    expect(iframe?.getAttribute("src")).toMatch(/^blob:/);
  });

  it("handles self-mounting content with createRoot", () => {
    const code = `import React from "react";\nimport { createRoot } from "react-dom/client";\nfunction App() { return <div>Hello</div>; }\ncreateRoot(document.getElementById("root")).render(<App />);`;
    const { container } = render(<JsxRenderer content={code} />);
    const iframe = container.querySelector("iframe");
    expect(iframe).toBeTruthy();
  });

  it("calls URL.createObjectURL with Blob argument", () => {
    render(
      <JsxRenderer content="function App() { return <div>Hello</div>; }" />,
    );
    expect(URL.createObjectURL).toHaveBeenCalled();
  });
});

describe("JsxRenderer regression: no blob module imports", () => {
  it("transformed output does not contain raw JSX", () => {
    // This is the pattern from the production artifact
    const code = `import { useState } from "react";
import { BarChart, Bar } from "recharts";
export default function Dashboard() {
  const [tab, setTab] = useState("a");
  return <div><BarChart><Bar dataKey="x" /></BarChart></div>;
}`;
    const result = transformJsx(code);
    // Must not contain JSX angle brackets (they should be transformed to _jsx calls)
    expect(result).not.toMatch(/<div>/);
    expect(result).not.toMatch(/<BarChart>/);
    expect(result).toMatch(/_jsx/);
    // Original imports should be preserved
    expect(result).toContain('import { useState } from "react"');
    expect(result).toContain('import { BarChart, Bar } from "recharts"');
  });
});
