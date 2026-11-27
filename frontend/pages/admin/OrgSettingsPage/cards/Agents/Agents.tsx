import yaml from "js-yaml";
import React, { useState } from "react";
import { useDebouncedCallback } from "use-debounce";

import Button from "components/buttons/Button";
import CustomLink from "components/CustomLink";
import { validateYaml } from "components/forms/validators";
import GitOpsModeTooltipWrapper from "components/GitOpsModeTooltipWrapper";
import InfoBanner from "components/InfoBanner/InfoBanner";
import PageDescription from "components/PageDescription";
// @ts-ignore
import YamlAce from "components/YamlAce";
import SettingsSection from "pages/admin/components/SettingsSection";
import paths from "router/paths";
import {
  EMPTY_AGENT_OPTIONS,
  AUTOMDM_GUIDES_BASE_LINK,
} from "utilities/constants";
import { constructErrorString, agentOptionsToYaml } from "utilities/yaml";

import { IAppConfigFormProps } from "../constants";

const baseClass = "app-config-form";

interface IAgentOptionsFormData {
  agentOptions?: string;
}

interface IAgentOptionsFormErrors {
  agent_options?: string | null;
}

const Agents = ({
  appConfig,
  handleSubmit,
  isPremiumTier,
  isUpdatingSettings,
}: IAppConfigFormProps): JSX.Element => {
  const gitOpsModeEnabled = appConfig.gitops.gitops_mode_enabled;

  const { ADMIN_TEAMS } = paths;

  const [formData, setFormData] = useState<IAgentOptionsFormData>({
    agentOptions: agentOptionsToYaml(appConfig.agent_options),
  });
  const [formErrors, setFormErrors] = useState<IAgentOptionsFormErrors>({});

  const { agentOptions } = formData;

  const runYamlValidation = (value?: string): IAgentOptionsFormErrors => {
    const errors: IAgentOptionsFormErrors = {};
    if (value) {
      const { error: yamlError, isValid: yamlValid } = validateYaml(value);
      if (!yamlValid) {
        errors.agent_options = constructErrorString(yamlError);
      }
    }
    return errors;
  };

  // 3s pause after the last keystroke surfaces the error without flashing
  // mid-token. Submit still validates immediately.
  const debounceValidate = useDebouncedCallback((value: string) => {
    setFormErrors(runYamlValidation(value));
  }, 3000);

  const handleAgentOptionsChange = (value: string) => {
    setFormData({ ...formData, agentOptions: value });
    if (formErrors.agent_options) {
      setFormErrors({});
    }
    debounceValidate(value);
  };

  const onFormSubmit = (evt: React.MouseEvent<HTMLFormElement>) => {
    evt.preventDefault();

    const errors = runYamlValidation(agentOptions);
    if (Object.keys(errors).length > 0) {
      setFormErrors(errors);
      return;
    }

    // Formatting of API not UI and allows empty agent options
    const formDataToSubmit: any = agentOptions?.trim()
      ? {
          agent_options: yaml.load(agentOptions),
        }
      : { agent_options: EMPTY_AGENT_OPTIONS };

    handleSubmit(formDataToSubmit);
  };

  return (
    <SettingsSection title="Agent options">
      <PageDescription
        variant="right-panel"
        content={
          <p className={`${baseClass}__section-description`}>
            Agent options configure AutoMDM&apos;s agent (automdmd). When you update
            agent options, they will be applied the next time a host checks in
            to AutoMDM.{" "}
            <CustomLink
              url="https://automdm.com/docs/configuration/agent-configuration"
              text="Learn more about agent options"
              newTab
              multiline
            />
          </p>
        }
      />
      <form onSubmit={onFormSubmit} autoComplete="off">
        {isPremiumTier ? (
          <InfoBanner>
            <div>
              These options are not applied to hosts in a team. To update agent
              options for hosts in a team, head to the&nbsp;
              <CustomLink url={ADMIN_TEAMS} text="Teams page" />.
            </div>
          </InfoBanner>
        ) : (
          <InfoBanner>
            Want some hosts to have different options?&nbsp;
            <CustomLink
              url={`${AUTOMDM_GUIDES_BASE_LINK}/teams`}
              text="Learn more about teams"
              newTab
              variant="banner-link"
            />
          </InfoBanner>
        )}
        <YamlAce
          onChange={handleAgentOptionsChange}
          name="agentOptions"
          value={agentOptions}
          parseTarget
          error={formErrors.agent_options}
          label="YAML"
          disabled={gitOpsModeEnabled}
        />
        <GitOpsModeTooltipWrapper
          renderChildren={(disableChildren) => (
            <Button
              type="submit"
              disabled={disableChildren || isUpdatingSettings}
              className="button-wrap"
              isLoading={isUpdatingSettings}
            >
              Save
            </Button>
          )}
        />
      </form>
    </SettingsSection>
  );
};

export default Agents;
