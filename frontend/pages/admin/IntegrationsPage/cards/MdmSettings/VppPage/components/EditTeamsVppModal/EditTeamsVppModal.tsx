import React, { useState } from "react";

import Button from "components/buttons/Button";
import Modal from "components/Modal";
import { notify } from "components/ToastNotification";
import { IMdmVppToken } from "interfaces/mdm";
import mdmAppleAPI from "services/entities/mdm_apple";

import VppTeamsSelector from "../VppTeamsSelector";
import { teamsForApi, valueFromToken } from "../VppTeamsSelector/helpers";

const baseClass = "edit-teams-vpp-modal";

interface IEditTeamsVppModalProps {
  currentToken: IMdmVppToken;
  onCancel: () => void;
  onSuccess: () => void;
}

const EditTeamsVppModal = ({
  currentToken,
  onCancel,
  onSuccess,
}: IEditTeamsVppModalProps) => {
  const [value, setValue] = useState(valueFromToken(currentToken));
  const [isSaving, setIsSaving] = useState(false);

  const onSave = async (evt: React.FormEvent<HTMLFormElement>) => {
    evt.preventDefault();
    setIsSaving(true);
    try {
      await mdmAppleAPI.editVppTeams({
        tokenId: currentToken.id,
        teamIds: teamsForApi(value),
      });
      notify.success("Edited successfully.");
      onSuccess();
    } catch (e) {
      notify.error("Couldn’t edit. Please try again.", { response: e });
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <Modal
      className={baseClass}
      title="Edit teams"
      onExit={onCancel}
      width="large"
      isContentDisabled={isSaving}
    >
      <form
        onSubmit={onSave}
        className={`${baseClass}__form`}
        autoComplete="off"
      >
        <p>
          Edit teams for <b>{currentToken.org_name}</b>.
        </p>
        <VppTeamsSelector
          value={value}
          onChange={setValue}
          tooltipPosition="top-start"
          showUncheckHelpText
        />
        <div className="modal-cta-wrap">
          <Button
            type="submit"
            className="save-vpp-teams-loading"
            isLoading={isSaving}
          >
            Save
          </Button>
        </div>
      </form>
    </Modal>
  );
};

export default EditTeamsVppModal;
