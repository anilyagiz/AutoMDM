import { Meta, StoryObj } from "@storybook/react";

import AutoMDMMarkdown from "./AutoMDMMarkdown";

const TestMarkdown = `
# Test Markdown

## This is a heading

### This is a subheading

#### This is a subsubheading


---
**bold**

*italic*

[test link](https://automdm.com)

- test list item 1
- test list item 2
- test list item 3

> test blockquote

\`code text\`
`;

const meta: Meta<typeof AutoMDMMarkdown> = {
  title: "Components/AutoMDMMarkdown",
  component: AutoMDMMarkdown,
  args: { markdown: TestMarkdown },
};

export default meta;

type Story = StoryObj<typeof AutoMDMMarkdown>;

export const Basic: Story = {};
