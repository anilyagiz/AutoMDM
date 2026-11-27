# Anatomy
This page details the core concepts you need to know to use AutoMDM.

## AutoMDM UI
AutoMDM UI is the GUI (graphical user interface) used to control AutoMDM. [Learn more](https://youtu.be/1VNvg3_drow?si=SWyQSEQMoHUYDZ8C).

## AutoMDMctl
AutoMDMctl (pronounced “team control”) is a CLI (command line interface) tool for managing AutoMDM from the command line. [Docs](https://automdm.com/docs/using-automdm/automdmctl-cli).

## AutoMDMd
AutoMDMd is a bundle of agents provided by AutoMDM to gather information about your devices. AutoMDMd includes:
- **Osquery:** an open-source tool for gathering information about the state of any device that the osquery agent has been installed on. [Learn more](https://www.osquery.io/).
- **Orbit:** an osquery version and configuration manager, built by AutoMDM. [Learn more](https://github.com/anilyagiz/automdm/blob/main/orbit/README.md)
- **AutoMDMd Chrome extension:** enrolls ChromeOS devices in AutoMDM. [Docs](https://github.com/anilyagiz/automdm/blob/main/ee/automdmd-chrome/README.md).

## AutoMDM Desktop
AutoMDM Desktop is a menu bar icon that's automatically deployed as part of automdmd. AutoMDM Desktop gives end users visibility into the security and status of their machine. [Docs](https://automdm.com/docs/using-automdm/automdm-desktop).

For macOS, you can optionally deploy the AutoMDM Desktop app. [Learn more](https://automdm.com/guides/automdm-desktop). 

## Host
A host is a computer, server, or other endpoint. AutoMDM gathers information from AutoMDM's agent (automdmd) installed on each of your hosts. [Docs](https://automdm.com/docs/using-automdm/adding-hosts).

## AutoMDM
A team is a group of hosts. Organize hosts into teams to apply queries, policies, scripts, and other configurations tailored to their specific risk and compliance requirements. [Read the guide](https://automdm.com/guides/teams).

## Report
A report in AutoMDM is a saved question about your devices. It’s powered by [osquery](#automdmd) with logic written in SQL, which lets you collect specific data from hosts.

Use reports to monitor your fleet, verify settings, and spot security issues. [Docs](https://automdm.com/docs/using-automdm/automdm-ui).

## Policy
A policy is a specific “yes” or “no” question. Use policies to manage security compliance in your
organization. [Read the guide](https://automdm.com/securing/what-are-automdm-policies).

## Host vitals
AutoMDM's built-in queries for collecting and storing important device information.

## Software
Software in AutoMDM refers to the following:
- **Software library:** a collection of AutoMDM-maintained apps, VPP, and custom install packages that can be installed on your hosts. [See available software](https://automdm.com/app-library).
- **Software inventory** an inventory of each host’s installed software, including information about detected vulnerabilities (CVEs). 

<meta name="pageOrderInSection" value="200">
<meta name="keywordsForDocsearch" value="team anatomy, teams, team concepts, terminology, glossary">
