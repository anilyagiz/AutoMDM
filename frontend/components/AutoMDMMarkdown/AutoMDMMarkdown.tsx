import { Ace } from "ace-builds";
import classnames from "classnames";
import { noop } from "lodash";
import React from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import CustomLink from "components/CustomLink";
import SQLEditor from "components/SQLEditor";

interface IAutoMDMMarkdownProps {
  markdown: string;
  className?: string;
  name?: string;
}

const baseClass = "automdm-markdown";

/** This will give us sensible defaults for how we render markdown across the team application.
 * NOTE: can be extended later to take custom components, but dont need that at the moment.
 */
const AutoMDMMarkdown = ({ markdown, className, name }: IAutoMDMMarkdownProps) => {
  const classNames = classnames(baseClass, className);

  return (
    <div className={classNames}>
      <ReactMarkdown
        // enables some more markdown features such as direct urls and strikethroughs.
        // more info here: https://github.com/remarkjs/remark-gfm
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ href = "", children }) => {
            return <CustomLink text={String(children)} url={href} newTab />;
          },

          // handles code blocks
          pre: ({ children }) => {
            const onEditorBlur = (editor?: Ace.Editor) => {
              editor && editor.clearSelection();
            };

            const onEditorLoad = (editor: Ace.Editor) => {
              editor.setOptions({
                indentedSoftWrap: false, // removes automatic indentation when wrapping
              });

              // removes focus UI styling
              editor.renderer.visualizeFocus = noop;
            };

            // Extract the text content from the code element inside pre
            // children is typically <code>...</code>
            let codeContent = "";
            if (React.isValidElement(children)) {
              const codeChildren = children.props?.children;
              codeContent = String(codeChildren || "");
            } else {
              codeContent = String(children || "");
            }

            // full code blocks we want to use AutoMDM Ace.
            // e.g. ```SELECT * FROM USERS```
            return (
              <pre>
                <SQLEditor
                  wrapperClassName={`${baseClass}__ace-display`}
                  // Remove trailing newline added by markdown parser, preserving newlines within the code block
                  value={codeContent.replace(/\n$/, "")}
                  showGutter={false}
                  onBlur={onEditorBlur}
                  onLoad={onEditorLoad}
                  style={{ border: "none" }}
                  wrapEnabled
                  readOnly
                  name={name}
                />
              </pre>
            );
          },

          // Inline code only (since block code is now handled by `pre`)
          code: ({ children, ...props }) => {
            return <code {...props}>{children}</code>;
          },
        }}
      >
        {markdown}
      </ReactMarkdown>
    </div>
  );
};

export default AutoMDMMarkdown;
