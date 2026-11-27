import PATHS from "router/paths";

import { ISideNavItem } from "../components/SideNav/SideNav";

import Advanced from "./cards/Advanced";
import Agents from "./cards/Agents";
import { IAppConfigFormProps } from "./cards/constants";
import AutoMDMDesktop from "./cards/AutoMDMDesktop";
import Info from "./cards/Info";
import Smtp from "./cards/Smtp";
import Statistics from "./cards/Statistics";
import WebAddress from "./cards/WebAddress";

const ORG_SETTINGS_NAV_ITEMS: ISideNavItem<IAppConfigFormProps>[] = [
  {
    title: "Organization info",
    urlSection: "organization",
    path: PATHS.ADMIN_ORGANIZATION_INFO,
    Card: Info,
  },
  {
    title: "AutoMDM web address",
    urlSection: "webaddress",
    path: PATHS.ADMIN_ORGANIZATION_WEBADDRESS,
    Card: WebAddress,
  },
  {
    title: "SMTP options",
    urlSection: "smtp",
    path: PATHS.ADMIN_ORGANIZATION_SMTP,
    Card: Smtp,
  },
  {
    title: "Agent options",
    urlSection: "agents",
    path: PATHS.ADMIN_ORGANIZATION_AGENTS,
    Card: Agents,
  },
  {
    title: "Usage statistics",
    urlSection: "statistics",
    path: PATHS.ADMIN_ORGANIZATION_STATISTICS,
    Card: Statistics,
  },
  {
    title: "AutoMDM Desktop",
    urlSection: "automdm-desktop",
    path: PATHS.ADMIN_ORGANIZATION_AUTOMDM_DESKTOP,
    // isPremium: true,
    Card: AutoMDMDesktop,
  },
  {
    title: "Advanced options",
    urlSection: "advanced",
    path: PATHS.ADMIN_ORGANIZATION_ADVANCED,
    Card: Advanced,
  },
];

export default ORG_SETTINGS_NAV_ITEMS;
