import React from "react";

import AutoMDMMarkdown from "components/AutoMDMMarkdown";

interface IQueryTableExampleProps {
  example: string;
}

const baseClass = "query-table-example";

const QueryTableExample = ({ example }: IQueryTableExampleProps) => {
  return (
    <div className={baseClass}>
      <h3>Example</h3>
      <AutoMDMMarkdown markdown={example} name="query-table-example" />
    </div>
  );
};

export default QueryTableExample;
