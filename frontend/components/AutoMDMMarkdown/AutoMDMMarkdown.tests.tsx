import { render, screen } from "@testing-library/react";
import React from "react";

import AutoMDMMarkdown from "./AutoMDMMarkdown";

jest.mock("components/SQLEditor", () => ({
  __esModule: true,
  default: ({ value }: { value: string }) => (
    <pre data-testid="sql-editor">{value}</pre>
  ),
}));

/**
 * Tests AutoMDM-specific rendering behavior in the AutoMDMMarkdown wrapper:
 * CustomLink integration, SQLEditor code block delegation, and inline
 * code passthrough.
 */
describe("AutoMDMMarkdown", () => {
  it("renders plain text", () => {
    render(<AutoMDMMarkdown markdown="Hello world" />);
    expect(screen.getByText("Hello world")).toBeInTheDocument();
  });

  it("renders links via CustomLink (opens in new tab)", () => {
    render(<AutoMDMMarkdown markdown="Visit [AutoMDM](https://automdm.com)" />);
    const link = screen.getByRole("link", { name: /AutoMDM/i });
    expect(link).toHaveAttribute("href", "https://automdm.com");
    expect(link).toHaveAttribute("target", "_blank");
  });

  it("renders code blocks through SQLEditor", () => {
    const md = "```\nSELECT * FROM users\n```";
    render(<AutoMDMMarkdown markdown={md} />);
    expect(screen.getByTestId("sql-editor")).toHaveTextContent(
      "SELECT * FROM users"
    );
  });

  it("renders inline code without SQLEditor", () => {
    const { container } = render(
      <AutoMDMMarkdown markdown="Run `automdmctl apply`" />
    );
    const code = container.querySelector("code");
    expect(code?.textContent).toBe("automdmctl apply");
    expect(screen.queryByTestId("sql-editor")).not.toBeInTheDocument();
  });
});
