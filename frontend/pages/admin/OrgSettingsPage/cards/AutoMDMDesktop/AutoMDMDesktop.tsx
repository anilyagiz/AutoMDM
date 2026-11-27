import React, { useState } from "react";

import Button from "components/buttons/Button";
import CustomLink from "components/CustomLink";
import InputField from "components/forms/fields/InputField";
import Radio from "components/forms/fields/Radio";
import { isValidHostname, isValidURL } from "components/forms/validators";
import GitOpsModeTooltipWrapper from "components/GitOpsModeTooltipWrapper";
import PageDescription from "components/PageDescription";
import { IInputFieldParseTarget } from "interfaces/form_field";
import SettingsSection from "pages/admin/components/SettingsSection";
import { isEndUserIdPConfigured } from "utilities/permissions/permissions";

import TooltipWrapper from "../../../../../components/TooltipWrapper";
import { DEFAULT_TRANSPARENCY_URL, IAppConfigFormProps } from "../constants";

const END_USER_AUTH_LABEL_ID = "end-user-authentication-label";

const SSO_REQUIRES_IDP_TOOLTIP =
  "This setting requires an IdP configured in Settings > Integrations > Authentication (SSO) > End users.";

const SSO_TOKEN_ROTATION_TOOLTIP =
  "AutoMDM still rotates the token. SSO authentication is added on top.";

enum EndUserAuthType {
  TOKEN_ROTATION = "token_rotation",
  SSO = "sso",
}

interface IAutoMDMDesktopFormData {
  transparencyURL: string;
  alternativeBrowserHost: string;
  ssoEnabled: boolean;
}
interface IAutoMDMDesktopFormErrors {
  transparencyURL?: string | null;
  alternativeBrowserHost?: string | null;
}

const AutoMDMDesktop = ({
  appConfig,
  handleSubmit,
  isPremiumTier,
  isUpdatingSettings,
}: IAppConfigFormProps): JSX.Element => {
  const gitOpsModeEnabled = appConfig.gitops.gitops_mode_enabled;

  const [formData, setFormData] = useState<IAutoMDMDesktopFormData>({
    transparencyURL:
      appConfig.automdm_desktop?.transparency_url || DEFAULT_TRANSPARENCY_URL,
    alternativeBrowserHost:
      appConfig.automdm_desktop?.alternative_browser_host || "",
    ssoEnabled: appConfig.automdm_desktop?.sso_enabled ?? false,
  });

  const [formErrors, setFormErrors] = useState<IAutoMDMDesktopFormErrors>({});

  const onInputChange = ({ name, value }: IInputFieldParseTarget) => {
    setFormData((prevFormData) => ({
      ...prevFormData,
      [name]: value.toString(),
    }));
    setFormErrors((prevErrors) => {
      const newErrors = { ...prevErrors };
      delete newErrors[name as keyof IAutoMDMDesktopFormErrors];
      return newErrors;
    });
  };

  const onEndUserAuthChange = (value: string) => {
    setFormData((prevFormData) => ({
      ...prevFormData,
      ssoEnabled: value === EndUserAuthType.SSO,
    }));
  };

  const validateForm = () => {
    const { transparencyURL, alternativeBrowserHost } = formData;

    const errors: IAutoMDMDesktopFormErrors = {};

    if (
      transparencyURL &&
      !isValidURL({ url: transparencyURL, protocols: ["http", "https"] })
    ) {
      errors.transparencyURL = `Custom transparency URL must include protocol (e.g. https://)`;
    }

    if (alternativeBrowserHost && !isValidHostname(alternativeBrowserHost)) {
      errors.alternativeBrowserHost = `Browser host must be a valid hostname or IP address (e.g. example.com, 192.168.1.50) and may include a port.`;
    }

    setFormErrors(errors);
  };

  const getAlternativeBrowserHostUrlTooltip = () => (
    <>
      If you are using mTLS for your agent-server communication, specify an
      alternative host to direct AutoMDM Desktop through.{" "}
      <CustomLink
        url="https://automdm.com/learn-more-about/alternative-browser-host"
        text="Learn more "
        variant="tooltip-link"
        newTab
      />
    </>
  );

  const onFormSubmit = (evt: React.MouseEvent<HTMLFormElement>) => {
    evt.preventDefault();

    const formDataForAPI = {
      automdm_desktop: {
        transparency_url: formData.transparencyURL,
        alternative_browser_host: formData.alternativeBrowserHost,
        sso_enabled: formData.ssoEnabled,
      },
    };

    handleSubmit(formDataForAPI);
  };

  if (!isPremiumTier) {
    return <></>;
  }

  const isIdPConfigured = isEndUserIdPConfigured(appConfig);

  return (
    <SettingsSection title="AutoMDM Desktop">
      <PageDescription
        variant="right-panel"
        content="Customize the AutoMDM Desktop experience."
      />
      <form onSubmit={onFormSubmit} autoComplete="off">
        <InputField
          label="Custom transparency URL"
          onChange={onInputChange}
          name="transparencyURL"
          value={formData.transparencyURL}
          parseTarget
          onBlur={validateForm}
          error={formErrors.transparencyURL}
          placeholder="https://automdm.com/transparency"
          disabled={gitOpsModeEnabled}
          helpText={
            <>
              {" "}
              By default, end users who click &quot;About AutoMDM&quot; in the
              AutoMDM Desktop menu are taken to{" "}
              <CustomLink
                url="https://automdm.com/transparency"
                text="https://automdm.com/transparency"
                newTab
                multiline
              />{" "}
            </>
          }
        />
        <InputField
          label={
            <TooltipWrapper tipContent={getAlternativeBrowserHostUrlTooltip()}>
              Browser host
            </TooltipWrapper>
          }
          onChange={onInputChange}
          onBlur={validateForm}
          name="alternativeBrowserHost"
          value={formData.alternativeBrowserHost}
          parseTarget
          error={formErrors.alternativeBrowserHost}
          disabled={gitOpsModeEnabled}
          helpText="If not set, AutoMDM Desktop uses your AutoMDM web address."
        />
        <fieldset
          className="form-field"
          aria-labelledby={END_USER_AUTH_LABEL_ID}
        >
          <div className="form-field__label" id={END_USER_AUTH_LABEL_ID}>
            End user authentication
          </div>
          <Radio
            label="Hourly token rotation"
            id="end-user-auth-token-rotation"
            name="end-user-authentication"
            value={EndUserAuthType.TOKEN_ROTATION}
            checked={!formData.ssoEnabled}
            disabled={gitOpsModeEnabled}
            onChange={onEndUserAuthChange}
          />
          <Radio
            label={
              <TooltipWrapper
                showArrow
                underline={false}
                position="right"
                tipOffset={12}
                tipContent={
                  isIdPConfigured
                    ? SSO_TOKEN_ROTATION_TOOLTIP
                    : SSO_REQUIRES_IDP_TOOLTIP
                }
              >
                Single sign-on (SSO)
              </TooltipWrapper>
            }
            id="end-user-auth-sso"
            name="end-user-authentication"
            value={EndUserAuthType.SSO}
            checked={formData.ssoEnabled}
            disabled={gitOpsModeEnabled || !isIdPConfigured}
            onChange={onEndUserAuthChange}
          />
        </fieldset>
        <GitOpsModeTooltipWrapper
          renderChildren={(disableChildren) => (
            <Button
              type="submit"
              disabled={Object.keys(formErrors).length > 0 || disableChildren}
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

export default AutoMDMDesktop;
