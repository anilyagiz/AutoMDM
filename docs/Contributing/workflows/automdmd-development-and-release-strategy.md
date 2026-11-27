# AutoMDMd development and release strategy

Goal: Define strategy that AutoMDMd developers must follow when introducing new features.

## Why do we need a strategy?

AutoMDMd and AutoMDM use different release strategies. AutoMDMd components are updated via "automatic updates" by continuously polling https://tuf.fleetctl.com/ for new versions, whereas on-premises AutoMDM servers are updated manually by administrators.
For this reason we need a good release strategy to not break on-premise deployments when we release new versions of AutoMDMd components.

## Must rule

"New AutoMDMd versions always support communication + operation with older AutoMDM servers."

> Why is it a must?

As mentioned before, AutoMDMd uses an auto-update mechanism, whereas AutoMDM does not.
We don't want to break on-premise AutoMDM deployments, and we don't want to force AutoMDMd users to update their servers every time we push a new AutoMDMd update to AutoMDM's TUF server.

## Nice to have

Nice to have, but not a must: "New AutoMDM server versions support old versions of AutoMDMd."

> Why is it not a must?

This allows some flexibility when developing new features in AutoMDMd and AutoMDM.

## Release process

1. AutoMDMd components (Orbit, AutoMDM Desktop and osqueryd) must be released to AutoMDM's TUF before new AutoMDM server releases are available in Github.
2. When the new AutoMDM server version doesn't support older AutoMDMd versions (see [Nice to have](#nice-to-have)), the release notes must document their minimum supported AutoMDMd version. This is for users that use AutoMDMd with auto-updates disabled or they pin to a specific channel. These users would need to first update AutoMDMd on their devices and then proceed to upgrade AutoMDM server.

<meta name="pageOrderInSection" value="1200">
<meta name="description" value="An outline of the strategy that developers must follow when introducing new features to automdmd.">