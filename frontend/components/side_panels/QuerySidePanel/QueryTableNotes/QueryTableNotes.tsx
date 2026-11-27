import React from "react";

import AutoMDMMarkdown from "components/AutoMDMMarkdown";

interface IQueryTableNotesProps {
  notes: string;
}

const baseClass = "query-table-notes";

const QueryTableNotes = ({ notes }: IQueryTableNotesProps) => {
  return (
    <div className={baseClass}>
      <h3>Notes</h3>
      <AutoMDMMarkdown
        markdown={notes}
        className={`${baseClass}__notes-markdown`}
      />
    </div>
  );
};

export default QueryTableNotes;
