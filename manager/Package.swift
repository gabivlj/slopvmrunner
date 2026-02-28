// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "vmmanager",
    platforms: [.macOS(.v13)],
    products: [
        .executable(name: "vmmanager", targets: ["vmmanager"])
    ],
    targets: [
        .executableTarget(
            name: "vmmanager",
            linkerSettings: [
                .linkedFramework("Virtualization")
            ]
        )
    ]
)
