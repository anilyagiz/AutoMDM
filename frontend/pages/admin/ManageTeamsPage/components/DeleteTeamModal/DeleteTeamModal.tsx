import React from "react";

import Button from "components/buttons/Button";
import Modal from "components/Modal";

const baseClass = "delete-team-modal";

interface IDeleteAutoMDMModalProps {
  name: string;
  isUpdatingAutoMDMs: boolean;
  onSubmit: () => void;
  onCancel: () => void;
}

const DeleteTeamModal = ({
  name,
  isUpdatingAutoMDMs,
  onSubmit,
  onCancel,
}: IDeleteAutoMDMModalProps): JSX.Element => {
  return (
    <Modal
      title="Delete team"
      onExit={onCancel}
      onEnter={isUpdatingAutoMDMs ? undefined : onSubmit}
      className={baseClass}
    >
      <p>
        This will delete the{" "}
        <span className={`${baseClass}__name`}>{name}</span> team.
      </p>
      <p>
        Users on this team who are not assigned to other teams won&apos;t be
        able to log in.
      </p>
      <div className="modal-cta-wrap">
        <Button
          type="button"
          onClick={onSubmit}
          variant="alert"
          className="delete-loading"
          isLoading={isUpdatingAutoMDMs}
          disabled={isUpdatingAutoMDMs}
        >
          Delete
        </Button>
        <Button onClick={onCancel} variant="secondary">
          Cancel
        </Button>
      </div>
    </Modal>
  );
};

export default DeleteTeamModal;
