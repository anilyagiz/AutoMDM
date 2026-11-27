// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "AutoMDMDesktopMacOS",
    platforms: [
        .macOS(.v13)  // Set this to your target version
    ],
    targets: [
        .target(
            name: "AutoMDMDesktop",
            path: "AutoMDMDesktop"
        ),
        .target(
            name: "AutoMDMPSSOExtension",
            path: "AutoMDMPSSOExtension"
        ),
    ]
)
