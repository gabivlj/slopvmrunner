import Foundation
import Virtualization
import Darwin

enum BootMode: String {
    case linux
    case efi
}

struct ManagerConfig {
    let bootMode: BootMode
    let kernelPath: String?
    let initrdPath: String?
    let rootImagePath: String
    let memoryMiB: UInt64
    let cpuCount: Int
    let agentPort: Int
    let verbose: Bool

    var bootArgs: String {
        [
            "console=hvc0",
            "loglevel=7",
            "printk.time=1",
            "root=/dev/vda rw",
            "init=/sbin/agent",
            "agent.port=\(agentPort)"
        ].joined(separator: " ")
    }
}

final class VMRuntime {
    let vm: VZVirtualMachine
    let verbose: Bool
    private var stateLogTimer: DispatchSourceTimer?
    private var lastState: String?

    init(vm: VZVirtualMachine, verbose: Bool) {
        self.vm = vm
        self.verbose = verbose
    }

    func startStateLogging() {
        guard verbose else { return }
        let timer = DispatchSource.makeTimerSource(queue: DispatchQueue.global(qos: .utility))
        timer.schedule(deadline: .now(), repeating: .seconds(1))
        timer.setEventHandler { [weak self] in
            guard let self else { return }
            let state = String(describing: self.vm.state)
            if self.lastState != state {
                self.lastState = state
                fputs("[vmmanager] state=\(state)\n", stderr)
            }
        }
        timer.resume()
        self.stateLogTimer = timer
    }
}

@main
struct VMManagerCLI {
    static var runtime: VMRuntime?

    static func main() {
        do {
            let cfg = try parseArgs()
            try startVM(config: cfg)
            RunLoop.main.run()
        } catch {
            fputs("error: \(error)\n", stderr)
            exit(1)
        }
    }

    static func parseArgs() throws -> ManagerConfig {
        var bootMode: BootMode = .linux
        var kernelPath: String?
        var initrdPath: String?
        var rootImagePath: String?
        var memoryMiB: UInt64 = 512
        var cpuCount = 2
        var agentPort = 8080
        var verbose = false

        var i = 1
        while i < CommandLine.arguments.count {
            let arg = CommandLine.arguments[i]
            switch arg {
            case "--boot-mode":
                i += 1
                let raw = value(at: i, for: arg)
                guard let mode = BootMode(rawValue: raw) else {
                    throw CLIError.invalidArg("--boot-mode must be one of: linux, efi")
                }
                bootMode = mode
            case "--kernel":
                i += 1; kernelPath = value(at: i, for: arg)
            case "--initrd":
                i += 1; initrdPath = value(at: i, for: arg)
            case "--root-image":
                i += 1; rootImagePath = value(at: i, for: arg)
            case "--memory-mib":
                i += 1; memoryMiB = UInt64(value(at: i, for: arg)) ?? memoryMiB
            case "--cpus":
                i += 1; cpuCount = Int(value(at: i, for: arg)) ?? cpuCount
            case "--agent-port":
                i += 1; agentPort = Int(value(at: i, for: arg)) ?? agentPort
            case "--verbose":
                verbose = true
            case "--help", "-h":
                usageAndExit(code: 0)
            default:
                throw CLIError.invalidArg(arg)
            }
            i += 1
        }

        guard let rootImagePath else {
            usageAndExit(code: 1, message: "missing required --root-image")
        }

        if bootMode == .linux && kernelPath == nil {
            usageAndExit(code: 1, message: "missing required --kernel for linux boot mode")
        }

        return ManagerConfig(
            bootMode: bootMode,
            kernelPath: kernelPath,
            initrdPath: initrdPath,
            rootImagePath: rootImagePath,
            memoryMiB: memoryMiB,
            cpuCount: cpuCount,
            agentPort: agentPort,
            verbose: verbose
        )
    }

    static func value(at idx: Int, for arg: String) -> String {
        guard idx < CommandLine.arguments.count else {
            usageAndExit(code: 1, message: "missing value for \(arg)")
        }
        return CommandLine.arguments[idx]
    }

    static func usageAndExit(code: Int32, message: String? = nil) -> Never {
        if let message {
            fputs("\(message)\n", stderr)
        }
        let text = """
Usage: vmmanager --root-image <path> [options]

Options:
  --boot-mode <linux|efi> Default linux
  --kernel <path>         Required in linux mode
  --initrd <path>         Optional initrd path
  --memory-mib <int>      Default 512
  --cpus <int>            Default 2
  --agent-port <int>      Default 8080
  --verbose               Enable manager debug logs
  -h, --help              Show help
"""
        print(text)
        exit(code)
    }

    static func startVM(config: ManagerConfig) throws {
        let vmConfig = VZVirtualMachineConfiguration()
        vmConfig.cpuCount = max(1, config.cpuCount)
        vmConfig.memorySize = config.memoryMiB * 1024 * 1024

        switch config.bootMode {
        case .linux:
            guard let kernelPath = config.kernelPath else {
                throw CLIError.invalidArg("missing --kernel for linux boot mode")
            }
            try validateKernelArtifact(at: kernelPath)

            let bootLoader = VZLinuxBootLoader(kernelURL: URL(fileURLWithPath: kernelPath))
            if let initrd = config.initrdPath {
                bootLoader.initialRamdiskURL = URL(fileURLWithPath: initrd)
            }
            bootLoader.commandLine = config.bootArgs
            vmConfig.bootLoader = bootLoader
        case .efi:
            let bootLoader = VZEFIBootLoader()
            vmConfig.bootLoader = bootLoader

            if config.initrdPath != nil || config.kernelPath != nil {
                fputs("warning: --kernel/--initrd are ignored in efi boot mode\n", stderr)
            }
        }

        let diskAttachment = try VZDiskImageStorageDeviceAttachment(
            url: URL(fileURLWithPath: config.rootImagePath),
            readOnly: false
        )
        let disk = VZVirtioBlockDeviceConfiguration(attachment: diskAttachment)
        vmConfig.storageDevices = [disk]

        let serialAttachment = VZFileHandleSerialPortAttachment(
            fileHandleForReading: FileHandle.standardInput,
            fileHandleForWriting: FileHandle.standardOutput
        )
        let serial = VZVirtioConsoleDeviceSerialPortConfiguration()
        serial.attachment = serialAttachment
        vmConfig.serialPorts = [serial]

        let entropy = VZVirtioEntropyDeviceConfiguration()
        vmConfig.entropyDevices = [entropy]

        let netAttachment = VZNATNetworkDeviceAttachment()
        let netDevice = VZVirtioNetworkDeviceConfiguration()
        netDevice.attachment = netAttachment
        vmConfig.networkDevices = [netDevice]

        try vmConfig.validate()

        let vm = VZVirtualMachine(configuration: vmConfig)
        if config.verbose {
            let kernelDesc = config.kernelPath ?? "<none>"
            fputs("[vmmanager] bootMode=\(config.bootMode.rawValue) kernel=\(kernelDesc) rootImage=\(config.rootImagePath) memMiB=\(config.memoryMiB) cpus=\(config.cpuCount) agentPort=\(config.agentPort)\n", stderr)
            fputs("[vmmanager] kernelCmdline=\(config.bootArgs)\n", stderr)
            fputs("[vmmanager] serial console attached to stdio\n", stderr)
        }
        let runtime = VMRuntime(vm: vm, verbose: config.verbose)
        runtime.startStateLogging()
        self.runtime = runtime
        vm.start { result in
            switch result {
            case .success:
                print("vm started")
            case .failure(let error):
                fputs("failed to start vm: \(error)\n", stderr)
                exit(1)
            }
        }
    }

    static func validateKernelArtifact(at path: String) throws {
        let data = try Data(contentsOf: URL(fileURLWithPath: path))
        guard data.count >= 64 else {
            throw CLIError.invalidKernel("kernel file too small: \(path)")
        }

        if hostArch().hasPrefix("arm64") {
            let b0 = UInt32(data[56])
            let b1 = UInt32(data[57]) << 8
            let b2 = UInt32(data[58]) << 16
            let b3 = UInt32(data[59]) << 24
            let magic = b0 | b1 | b2 | b3
            let arm64ImageMagic: UInt32 = 0x644d5241 // "ARM\x64" little-endian

            if magic != arm64ImageMagic {
                throw CLIError.invalidKernel(
                    "kernel \(path) is not an ARM64 Linux Image (missing ARM64 image magic)."
                )
            }
        }
    }

    static func hostArch() -> String {
        var size: size_t = 0
        sysctlbyname("hw.machine", nil, &size, nil, 0)
        var machine = [CChar](repeating: 0, count: size)
        sysctlbyname("hw.machine", &machine, &size, nil, 0)
        return String(cString: machine)
    }
}

enum CLIError: Error {
    case invalidArg(String)
    case invalidKernel(String)
}
