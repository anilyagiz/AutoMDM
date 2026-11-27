#!/bin/sh

package_name=$PACKAGE_ID

# AutoMDM uninstalls app using product name that's extracted on upload
dnf remove --assumeyes "$package_name"
