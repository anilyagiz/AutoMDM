import classnames from "classnames";
import React, { useState, useCallback } from "react";

import { COLORS } from "styles/var/colors";

interface IAutoMDMAvatarProps {
  className?: string;
}

/**
 * a simple component that can be used to display a the AutoMDM logo as an avatar
 */
const AutoMDMAvatar = ({ className }: IAutoMDMAvatarProps) => {
  return (
    <svg
      data-testid="automdm-avatar"
      className={className}
      width="32"
      height="32"
      viewBox="0 0 32 32"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <circle
        cx="16"
        cy="16"
        r="15"
        stroke={COLORS["ui-automdm-black-25"]}
        strokeWidth="2"
      />
      {/* Placeholder AutoMDM mark until the final brand assets exist. */}
      <g transform="translate(16 16) scale(0.4) translate(-32.5 -32)">
        <path
          fill="#192147"
          fillRule="evenodd"
          d="M7 46 L16.5 18 L23.5 18 L33 46 L26.6 46 L24.7 40 L15.3 40 L13.4 46 Z M17 34.5 L23 34.5 L20 25 Z"
        />
        <path
          fill="#192147"
          d="M35 46 L35 18 L41.5 18 L46.5 31 L51.5 18 L58 18 L58 46 L52.5 46 L52.5 29.5 L48.6 39.5 L44.4 39.5 L40.5 29.5 L40.5 46 Z"
        />
      </g>
    </svg>
  );
};

interface IAPIOnlyAvatar {
  className?: string;
}

const APIOnlyAvatar = ({ className }: IAPIOnlyAvatar) => {
  return (
    <svg
      data-testid="apionly-avatar"
      className={className}
      width="32"
      height="32"
      viewBox="0 0 32 32"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <circle
        cx="16"
        cy="16"
        r="15"
        fill="none"
        stroke={COLORS["ui-automdm-black-25"]}
        strokeWidth="2"
      />
      <path
        d="M11.5 12.75L8 16L11.5 19.25"
        stroke={COLORS["ui-automdm-black-50"]}
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="M18 11L14.5 21"
        stroke={COLORS["ui-automdm-black-50"]}
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="M21 19.25L24.5 16L21 12.75"
        stroke={COLORS["ui-automdm-black-50"]}
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
};

const DefaultAvatar = ({ className }: IAPIOnlyAvatar) => {
  return (
    <svg
      data-testid="default-avatar"
      className={className}
      width="32"
      height="32"
      viewBox="0 0 32 32"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <circle
        cx="16"
        cy="16"
        r="15"
        stroke={COLORS["ui-automdm-black-25"]}
        strokeWidth="2"
      />
      <circle
        cx="16"
        cy="13"
        r="5.25"
        stroke={COLORS["ui-automdm-black-50"]}
        strokeWidth="1.5"
      />
      <path
        stroke={COLORS["ui-automdm-black-50"]}
        strokeWidth="1.5"
        d="M16 18.75c5.084 0 9.21 4.102 9.248 9.178-.01.017-.024.044-.052.08a2.945 2.945 0 0 1-.462.463c-.448.374-1.127.812-1.99 1.23-1.725.833-4.114 1.549-6.744 1.549s-5.019-.716-6.744-1.55c-.863-.417-1.542-.855-1.99-1.23a2.945 2.945 0 0 1-.462-.461c-.028-.037-.043-.064-.053-.081A9.25 9.25 0 0 1 16 18.75Z"
      />
    </svg>
  );
};

interface IAvatarUserInterface {
  gravatar_url?: string;
  gravatar_url_dark?: string;
}

interface IAvatarProps {
  className?: string;
  size?: string;
  user: IAvatarUserInterface;
  hasWhiteBackground?: boolean;
  /**
   * Set this to `true` to use the team avatar instead of the users gravatar.
   */
  useAutoMDMAvatar?: boolean;
  // Set this to true to show the API Only avatar
  useApiOnlyAvatar?: boolean;
}

const baseClass = "avatar";

const Avatar = ({
  className,
  size,
  user,
  hasWhiteBackground,
  useApiOnlyAvatar = false,
  useAutoMDMAvatar = false,
}: IAvatarProps) => {
  const [isLoading, setIsLoading] = useState(true);
  const [isError, setIsError] = useState(false);

  const onLoad = useCallback(() => {
    setIsLoading(false);
  }, []);
  const onError = useCallback(() => {
    setIsError(true);
  }, []);

  const avatarClasses = classnames(baseClass, className, {
    [`${baseClass}--${size?.toLowerCase()}`]: !!size,
    "has-white-background": !!hasWhiteBackground && !useApiOnlyAvatar,
  });
  const { gravatar_url } = user;

  let avatar;

  if (useAutoMDMAvatar) {
    avatar = <AutoMDMAvatar className={avatarClasses} />;
  } else if (useApiOnlyAvatar) {
    avatar = <APIOnlyAvatar className={avatarClasses} />;
  } else if (gravatar_url) {
    avatar = (
      <img
        alt="User avatar"
        className={`${avatarClasses} ${isLoading || isError ? "default" : ""}`}
        src={gravatar_url}
        onError={onError}
        onLoad={onLoad}
      />
    );
  } else {
    avatar = <DefaultAvatar className={avatarClasses} />;
  }

  return <div className="avatar-wrapper">{avatar}</div>;
};

export default Avatar;
