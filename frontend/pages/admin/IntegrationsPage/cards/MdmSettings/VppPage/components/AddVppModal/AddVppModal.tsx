import React, { useCallback, useContext, useState } from "react";

import Button from "components/buttons/Button";
import CustomLink from "components/CustomLink";
import FileUploader from "components/FileUploader";
import Modal from "components/Modal";
import { notify } from "components/ToastNotification";
import { AppContext } from "context/app";
import mdmAppleAPI from "services/entities/mdm_apple";

import VppTeamsSelector from "../VppTeamsSelector";
import { teamsForApi, IVppTeamsValue } from "../VppTeamsSelector/helpers";

import { getErrorMessage } from "./helpers";

const baseClass = "add-vpp-modal";

interface IAddVppModalProps {
  onCancel: () => void;
  onAdded: () => void;
}

const LEARN_MORE_URL = "https://automdm.com/learn-more-about/add-vpp";
const TEAMS_LEARN_MORE_URL =
  "https://automdm.com/learn-more-about/vpp-tokens-best-practice";

const AddVppModal = ({ onCancel, onAdded }: IAddVppModalProps) => {
  const { config } = useContext(AppContext);
  const gitOpsModeEnabled = config?.gitops.gitops_mode_enabled;

  const [tokenFile, setTokenFile] = useState<File | null>(null);
  const [teamsValue, setTeamsValue] = useState<IVppTeamsValue>({
    allAutoMDMs: true,
    automdmIds: [],
  });
  const [isUploading, setIsUploading] = useState(false);

  const onSelectFile = useCallback((files: FileList | null) => {
    const file = files?.[0];
    if (file) {
      setTokenFile(file);
    }
  }, []);

  const uploadVppToken = useCallback(async () => {
    setIsUploading(true);
    if (!tokenFile) {
      setIsUploading(false);
      notify.error("No token selected.");
      return;
    }

    try {
      const { token } = await mdmAppleAPI.uploadVppToken(tokenFile);
      const teams = teamsForApi(teamsValue);
      // The new token is unassigned, so Custom with no teams needs no PATCH.
      if (teams !== null && !gitOpsModeEnabled) {
        try {
          await mdmAppleAPI.editVppTeams({
            tokenId: token.id,
            teamIds: teams,
          });
        } catch (e) {
          notify.error(
            "Couldn't assign teams. The VPP token was added. Try again from Actions > Edit teams.",
            { response: e }
          );
          onAdded();
          return;
        }
      }
      notify.success("Added successfully.");
      onAdded();
    } catch (e) {
      notify.error(getErrorMessage(e), { response: e });
      onCancel();
    } finally {
      setIsUploading(false);
    }
  }, [tokenFile, teamsValue, gitOpsModeEnabled, onAdded, onCancel]);

  return (
    <Modal
      className={baseClass}
      title="Add VPP"
      onExit={onCancel}
      width="large"
    >
      <p className={`${baseClass}__description`}>
        Follow the step-by-step guide to add VPP.{" "}
        <CustomLink url={LEARN_MORE_URL} text="Learn more" newTab />
      </p>
      <div className={`${baseClass}__form`}>
        <FileUploader
          className={`${baseClass}__file-uploader ${
            isUploading ? `${baseClass}__file-uploader--loading` : ""
          }`}
          accept=".vpptoken"
          message="Content token (.vpptoken)"
          graphicName="file-vpp"
          buttonType="secondary"
          buttonMessage={isUploading ? "Uploading..." : "Upload"}
          fileDetails={tokenFile ? { name: tokenFile.name } : undefined}
          onFileUpload={onSelectFile}
        />
        {!gitOpsModeEnabled && (
          <VppTeamsSelector
            value={teamsValue}
            onChange={setTeamsValue}
            tooltipPosition="bottom-start"
            tooltipLearnMoreUrl={TEAMS_LEARN_MORE_URL}
            disabled={isUploading}
          />
        )}
      </div>
      <div className="modal-cta-wrap">
        <Button
          onClick={uploadVppToken}
          isLoading={isUploading}
          disabled={!tokenFile || isUploading}
        >
          Add VPP
        </Button>
      </div>
    </Modal>
  );
};

export default AddVppModal;
