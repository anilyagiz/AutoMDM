import { render } from "@testing-library/react";
import React from "react";

import {
  ensurePeriod,
  formatAlreadyAvailableInstallMessage,
  formatDifferentFileTypeMessage,
  ADD_SOFTWARE_ERROR_PREFIX,
} from "./helpers";

// --- ensurePeriod tests ---

describe("ensurePeriod", () => {
  it("adds a period to a string that doesn't end with a period", () => {
    expect(ensurePeriod("Test string")).toBe("Test string.");
  });

  it("returns the original string if it already ends with a period", () => {
    expect(ensurePeriod("Test string.")).toBe("Test string.");
  });

  it("returns an empty string unchanged", () => {
    expect(ensurePeriod("")).toBe("");
  });

  it("returns the original string if the string is only a period", () => {
    expect(ensurePeriod(".")).toBe(".");
  });
});

// --- formatAlreadyAvailableInstallMessage tests ---

describe("formatAlreadyAvailableInstallMessage", () => {
  it("formats the AutoMDM-maintained app conflict with bolded title and team", () => {
    const msg =
      "Zoom already has an AutoMDM-maintained app on the Testing & QA team.";
    const { container } = render(
      <>{formatAlreadyAvailableInstallMessage(msg)}</>
    );
    expect(container.textContent).toBe(
      "Couldn't add. Zoom already has an AutoMDM-maintained app on the Testing & QA team."
    );
    const bolds = container.querySelectorAll("b");
    expect(bolds).toHaveLength(2);
    expect(bolds[0].textContent).toBe("Zoom");
    expect(bolds[1].textContent).toBe("Testing & QA");
  });

  it("formats the Apple App Store (VPP) conflict with bolded title and team", () => {
    const msg =
      "Zoom already has an Apple App Store (VPP) on the Testing & QA team.";
    const { container } = render(
      <>{formatAlreadyAvailableInstallMessage(msg)}</>
    );
    expect(container.textContent).toBe(
      "Couldn't add. Zoom already has an Apple App Store (VPP) on the Testing & QA team."
    );
    const bolds = container.querySelectorAll("b");
    expect(bolds).toHaveLength(2);
    expect(bolds[0].textContent).toBe("Zoom");
    expect(bolds[1].textContent).toBe("Testing & QA");
  });

  it("formats the software package conflict with bolded title and team", () => {
    const msg =
      "Zoom already has a software package on the Testing & QA team.";
    const { container } = render(
      <>{formatAlreadyAvailableInstallMessage(msg)}</>
    );
    expect(container.textContent).toBe(
      "Couldn't add. Zoom already has a software package on the Testing & QA team."
    );
  });

  it("formats the package-limit conflict with bolded title and preserved count", () => {
    const msg =
      "AutoMDM osquery already has 10 versions. Before adding, delete one you no longer use.";
    const { container } = render(
      <>{formatAlreadyAvailableInstallMessage(msg)}</>
    );
    expect(container.textContent).toBe(
      "Couldn't add. AutoMDM osquery already has 10 versions. Before adding, delete one you no longer use."
    );
    const bolds = container.querySelectorAll("b");
    expect(bolds).toHaveLength(1);
    expect(bolds[0].textContent).toBe("AutoMDM osquery");
  });

  it("falls back to the legacy 'installer available' copy when the backend still emits it", () => {
    const msg = `${ADD_SOFTWARE_ERROR_PREFIX} MyApp already has an installer available for the Marketing team.`;
    const { container } = render(
      <>{formatAlreadyAvailableInstallMessage(msg)}</>
    );
    expect(container.textContent).toContain("Couldn't add.");
    expect(container.textContent).toContain("MyApp");
    expect(container.textContent).toContain("Marketing team");
  });

  it("strips the legacy 'Couldn't add software.' prefix before matching", () => {
    const msg = `Couldn't add software. Zoom already has an AutoMDM-maintained app on the Testing & QA team.`;
    const result = formatAlreadyAvailableInstallMessage(msg);
    const { container } = render(<>{result}</>);
    expect(container.textContent).toBe(
      "Couldn't add. Zoom already has an AutoMDM-maintained app on the Testing & QA team."
    );
  });

  it("handles the legacy quote-style 'SoftwareInstaller/In-house app ... already exists with team' format", () => {
    const msg = `SoftwareInstaller "MyApp" already exists with team "Marketing".`;
    const { container } = render(
      <>{formatAlreadyAvailableInstallMessage(msg)}</>
    );
    expect(container.textContent).toContain("Couldn't add.");
    expect(container.textContent).toContain("MyApp");
    expect(container.textContent).toContain("Marketing team");
  });

  it("returns null when the string doesn't match any known pattern", () => {
    expect(
      formatAlreadyAvailableInstallMessage("Random error not matching pattern")
    ).toBeNull();
  });

  it("returns null on an empty string", () => {
    expect(formatAlreadyAvailableInstallMessage("")).toBeNull();
  });
});

// --- formatDifferentFileTypeMessage tests ---

describe("formatDifferentFileTypeMessage", () => {
  it("formats the different-file-type message with the provided title bolded", () => {
    const msg = "The selected package is for a different file type.";
    const { container } = render(
      <>{formatDifferentFileTypeMessage(msg, "Zoom")}</>
    );
    expect(container.textContent).toBe(
      "Couldn't add. Zoom already has an installer of a different file type."
    );
    const bolds = container.querySelectorAll("b");
    expect(bolds).toHaveLength(1);
    expect(bolds[0].textContent).toBe("Zoom");
  });

  it("returns null when no software title is provided", () => {
    const msg = "The selected package is for a different file type.";
    expect(formatDifferentFileTypeMessage(msg, undefined)).toBeNull();
    expect(formatDifferentFileTypeMessage(msg, "")).toBeNull();
  });

  it("returns null when the reason doesn't include the sentinel", () => {
    expect(
      formatDifferentFileTypeMessage("Some other error", "Zoom")
    ).toBeNull();
  });
});
