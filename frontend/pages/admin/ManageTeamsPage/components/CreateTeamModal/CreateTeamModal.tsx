import React, { useState, useCallback, useEffect } from "react";

import Button from "components/buttons/Button";
import InputField from "components/forms/fields/InputField";
import Modal from "components/Modal";
import { ITeamFormData as IAutoMDMFormData } from "services/entities/teams";
import { MAX_ENTITY_CHAR_LENGTH } from "utilities/constants";

const baseClass = "create-team-modal";

interface ICreateTeamModalProps {
  onCancel: () => void;
  onSubmit: (formData: IAutoMDMFormData) => void;
  backendValidators: { [key: string]: string };
  isUpdatingAutoMDMs: boolean;
}

const CreateTeamModal = ({
  onCancel,
  onSubmit,
  backendValidators,
  isUpdatingAutoMDMs,
}: ICreateTeamModalProps): JSX.Element => {
  const [name, setName] = useState("");
  const [errors, setErrors] = useState<{ [key: string]: string }>(
    backendValidators
  );

  useEffect(() => {
    setErrors(backendValidators);
  }, [backendValidators]);

  const onInputChange = useCallback(
    (value: string) => {
      setName(value);
      setErrors({});
    },
    [setName]
  );

  const onFormSubmit = useCallback(
    (evt: React.FormEvent<HTMLFormElement>) => {
      evt.preventDefault();
      onSubmit({
        name: name.trim(),
      });
    },
    [onSubmit, name]
  );

  return (
    <Modal title="Add team" onExit={onCancel} className={baseClass}>
      <form
        className={`${baseClass}__form`}
        onSubmit={onFormSubmit}
        autoComplete="off"
      >
        <InputField
          autofocus
          name="name"
          onChange={onInputChange}
          onBlur={() => {
            setName(name.trim());
          }}
          label="Team name"
          placeholder="Workstations"
          value={name}
          error={errors.name}
          inputOptions={{ maxLength: MAX_ENTITY_CHAR_LENGTH }}
        />
        <div className="modal-cta-wrap">
          <Button
            type="submit"
            disabled={name.trim() === ""}
            className="create-loading"
            isLoading={isUpdatingAutoMDMs}
          >
            Create
          </Button>
          <Button onClick={onCancel} variant="secondary">
            Cancel
          </Button>
        </div>
      </form>
    </Modal>
  );
};

export default CreateTeamModal;
