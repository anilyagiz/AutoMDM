# AutoMDM development guides

This directory contains guides for common development tasks in AutoMDM.

## General guides

- [Seeding Data](seeding-data.md) - Guide for seeding data in AutoMDM
- [Committing Changes](committing-changes.md) - Guide for committing changes to AutoMDM
- [AutoMDMctl Apply](cli/automdmctl-apply.md) - Guide for using automdmctl apply
- [Simulate Slow Network](simulate-slow-network.md) - Guide for simulating a slow network
- [Troubleshooting Live Queries](troubleshooting-live-queries.md) - Guide for troubleshooting live queries
- [Enroll hosts with plain osquery](enroll-hosts-with-plain-osquery.md) - Guide for enrolling hosts with plain osquery
- [Upcoming activities](upcoming-activities.md) - Guide for managing upcoming host activities
- [mTLS reverse proxy setup](mtls-reverse-proxy-setup.md) - Guide for setting up an mTLS reverse proxy for testing
- [Okta conditional access testing](okta-conditional-access-testing.md) - Guide for testing Okta conditional access end-to-end in a dev environment
- [Smallstep ACME local setup](smallstep-acme-local-setup.md) - Guide for running step-ca locally with ngrok for ACME device attestation testing
- [Using AutoMDM as a Go module](using-automdm-as-a-go-module.md) - Guide for importing AutoMDM's Go packages into your own projects

## UI development

- [AutoMDM UI Testing](ui/automdm-ui-testing.md) - Guide for testing the AutoMDM UI
- [Generating UI Component Boilerplate](ui/generating-ui-component-boilerplate.md) - Guide for generating UI component boilerplate
- [Design QA Considerations](ui/design-qa-considerations.md) - Guide for design QA considerations

## Observability

- [Telemetry attribute naming](telemetry-attribute-naming.md) - Naming conventions for log, trace, and metric attributes

## API development

- [Adding New Endpoints](api/adding-new-endpoints.md) - Guide for adding new endpoints to the AutoMDM API

## Integration guides

- [SCIM Integration](integrations/scim-integration.md) - Guide for integrating AutoMDM with SCIM
- [Digicert Integration](integrations/digicert-integration.md) - Guide for integrating AutoMDM with Digicert