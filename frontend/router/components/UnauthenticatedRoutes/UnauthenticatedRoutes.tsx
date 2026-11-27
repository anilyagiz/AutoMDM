import React from "react";

interface IAppProps {
  children: JSX.Element;
}

export const UnauthenticatedRoutes = ({ children }: IAppProps): JSX.Element => {
  if (window.location.hostname.includes(".sandbox.automdm.com")) {
    window.location.href = "https://automdm.com/try-automdm/login";
  }
  return <div>{children}</div>;
};

export default UnauthenticatedRoutes;
