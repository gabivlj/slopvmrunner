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
    let agentVsockPort: Int
    let agentReadySocketPath: String?
    let enableNetwork: Bool
    let vmNetworkCIDR: String?
    let vmNetworkGateway: String?
    let vmNetworkIfName: String?
    let verbose: Bool

    var bootArgs: String {
        var args = [
            "console=hvc0",
            "loglevel=7",
            "printk.time=1",
            "root=/dev/vda rw",
            "init=/sbin/agent",
            "agent.vsock_port=\(agentVsockPort)"
        ]

        if enableNetwork {
            if let vmNetworkCIDR, !vmNetworkCIDR.isEmpty {
                args.append("agent.network_cidr=\(vmNetworkCIDR)")
            }
            if let vmNetworkGateway, !vmNetworkGateway.isEmpty {
                args.append("agent.network_gateway=\(vmNetworkGateway)")
            }
            if let vmNetworkIfName, !vmNetworkIfName.isEmpty {
                args.append("agent.network_ifname=\(vmNetworkIfName)")
            }
        }

        return args.joined(separator: " ")
    }
}

final class VMRuntime {
    let vm: VZVirtualMachine
    let config: ManagerConfig
    let verbose: Bool
    private var stateLogTimer: DispatchSourceTimer?
    private var lastState: String?
    private var vsockListener: VZVirtioSocketListener?
    private var vsockDelegate: AgentVsockListenerDelegate?

    init(vm: VZVirtualMachine, config: ManagerConfig, verbose: Bool) {
        self.vm = vm
        self.config = config
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

    func enableVsockAgentListener() {
        guard let socketDevice = vm.socketDevices.compactMap({ $0 as? VZVirtioSocketDevice }).first else {
            fputs("warning: no VZVirtioSocketDevice found; vsock listener disabled\n", stderr)
            return
        }

        let listener = VZVirtioSocketListener()
        let delegate = AgentVsockListenerDelegate(
            readySocketPath: config.agentReadySocketPath,
            verbose: verbose
        )
        listener.delegate = delegate
        socketDevice.setSocketListener(listener, forPort: UInt32(config.agentVsockPort))
        vsockListener = listener
        vsockDelegate = delegate

        if verbose {
            fputs("[vmmanager] vsock listening on host port \(config.agentVsockPort)\n", stderr)
            if let readyPath = config.agentReadySocketPath {
                fputs("[vmmanager] unix readiness notify path=\(readyPath)\n", stderr)
            }
        }
    }
}

final class AgentVsockListenerDelegate: NSObject, VZVirtioSocketListenerDelegate {
    private let readySocketPath: String?
    private let verbose: Bool
    private var readyNotified = false
    private var connections: [VZVirtioSocketConnection] = []
    private let lock = NSLock()

    init(readySocketPath: String?, verbose: Bool) {
        self.readySocketPath = readySocketPath
        self.verbose = verbose
    }

    func listener(
        _ listener: VZVirtioSocketListener,
        shouldAcceptNewConnection connection: VZVirtioSocketConnection,
        from socketDevice: VZVirtioSocketDevice
    ) -> Bool {
        lock.lock()
        defer { lock.unlock() }

        connections.append(connection)

        if verbose {
            fputs(
                "[vmmanager] accepted vsock connection src=\(connection.sourcePort) dst=\(connection.destinationPort)\n",
                stderr
            )
        }

        if !readyNotified {
            readyNotified = true
            sendConnectionFDOverUnixSocket(connection.fileDescriptor)
        }

        if verbose {
            fputs(
                "[vmmanager] notified parent process\n",
                stderr
            )
        }

        return true
    }

    private func sendConnectionFDOverUnixSocket(_ fdToSend: Int32) {
        guard let path = readySocketPath else { return }
        if path.utf8.count >= 104 {
            fputs("warning: unix socket path too long: \(path)\n", stderr)
            return
        }

        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else {
            fputs("warning: failed to create unix socket for readiness notify\n", stderr)
            return
        }
        defer { close(fd) }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let maxPathLen = MemoryLayout.size(ofValue: addr.sun_path)
        path.withCString { src in
            _ = withUnsafeMutablePointer(to: &addr.sun_path) {
                $0.withMemoryRebound(to: CChar.self, capacity: maxPathLen) { dst in
                    strncpy(dst, src, maxPathLen - 1)
                }
            }
        }

        let pathLen = path.utf8.count
        let len = socklen_t(MemoryLayout<sa_family_t>.size + pathLen + 1)
        let result = withUnsafePointer(to: &addr) { ptr in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockPtr in
                Darwin.connect(fd, sockPtr, len)
            }
        }
        if result != 0 {
            fputs("warning: failed to connect readiness socket at \(path): \(String(cString: strerror(errno)))\n", stderr)
            return
        }

        struct FDControlMessage {
            var header: cmsghdr
            var fd: Int32

            init(fd: Int32) {
                self.header = cmsghdr(
                    cmsg_len: socklen_t(MemoryLayout<cmsghdr>.size + MemoryLayout<Int32>.size),
                    cmsg_level: SOL_SOCKET,
                    cmsg_type: SCM_RIGHTS
                )
                self.fd = fd
            }
        }

        var payload: UInt8 = 0x1
        var control = FDControlMessage(fd: fdToSend)
        let sendResult: Int = withUnsafeMutablePointer(to: &payload) { payloadPtr in
            var iov = iovec(iov_base: UnsafeMutableRawPointer(payloadPtr), iov_len: 1)
            return withUnsafeMutablePointer(to: &control) { controlPtr in
                var msg = msghdr()
                msg.msg_iov = withUnsafeMutablePointer(to: &iov) { $0 }
                msg.msg_iovlen = 1
                msg.msg_control = UnsafeMutableRawPointer(controlPtr)
                msg.msg_controllen = socklen_t(MemoryLayout<FDControlMessage>.size)
                return Darwin.sendmsg(fd, &msg, 0)
            }
        }

        if sendResult < 0 {
            fputs("warning: sendmsg(SCM_RIGHTS) failed: \(String(cString: strerror(errno)))\n", stderr)
            return
        }

        if verbose {
            fputs("[vmmanager] delivered vsock fd \(fdToSend) over unix socket\n", stderr)
        }
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
        var agentVsockPort = 7000
        var agentReadySocketPath: String?
        var enableNetwork = true
        var vmNetworkCIDR: String?
        var vmNetworkGateway: String?
        var vmNetworkIfName: String?
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
            case "--agent-vsock-port":
                i += 1; agentVsockPort = Int(value(at: i, for: arg)) ?? agentVsockPort
            case "--agent-ready-socket":
                i += 1; agentReadySocketPath = value(at: i, for: arg)
            case "--enable-network":
                i += 1
                let raw = value(at: i, for: arg).lowercased()
                switch raw {
                case "true", "1", "yes", "on":
                    enableNetwork = true
                case "false", "0", "no", "off":
                    enableNetwork = false
                default:
                    throw CLIError.invalidArg("--enable-network must be true/false")
                }
            case "--vm-network-cidr":
                i += 1; vmNetworkCIDR = value(at: i, for: arg)
            case "--vm-network-gateway":
                i += 1; vmNetworkGateway = value(at: i, for: arg)
            case "--vm-network-ifname":
                i += 1; vmNetworkIfName = value(at: i, for: arg)
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
            agentVsockPort: agentVsockPort,
            agentReadySocketPath: agentReadySocketPath,
            enableNetwork: enableNetwork,
            vmNetworkCIDR: vmNetworkCIDR,
            vmNetworkGateway: vmNetworkGateway,
            vmNetworkIfName: vmNetworkIfName,
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
  --agent-vsock-port <int>  Default 7000
  --agent-ready-socket <path> Optional unix socket path for readiness notify
  --enable-network <bool>   Default true
  --vm-network-cidr <cidr>  Optional CIDR passed to guest cmdline
  --vm-network-gateway <ip> Optional gateway passed to guest cmdline
  --vm-network-ifname <name> Optional ifname passed to guest cmdline
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

        if config.enableNetwork {
            let netAttachment = VZNATNetworkDeviceAttachment()
            let netDevice = VZVirtioNetworkDeviceConfiguration()
            netDevice.attachment = netAttachment
            vmConfig.networkDevices = [netDevice]
        } else {
            vmConfig.networkDevices = []
        }

        let socketConfig = VZVirtioSocketDeviceConfiguration()
        vmConfig.socketDevices = [socketConfig]

        try vmConfig.validate()

        let vm = VZVirtualMachine(configuration: vmConfig)
        if config.verbose {
            let kernelDesc = config.kernelPath ?? "<none>"
            fputs("[vmmanager] bootMode=\(config.bootMode.rawValue) kernel=\(kernelDesc) rootImage=\(config.rootImagePath) memMiB=\(config.memoryMiB) cpus=\(config.cpuCount) agentVsockPort=\(config.agentVsockPort) enableNetwork=\(config.enableNetwork)\n", stderr)
            fputs("[vmmanager] kernelCmdline=\(config.bootArgs)\n", stderr)
            fputs("[vmmanager] serial console attached to stdio\n", stderr)
        }
        let runtime = VMRuntime(vm: vm, config: config, verbose: config.verbose)
        runtime.startStateLogging()
        self.runtime = runtime
        vm.start { result in
            switch result {
            case .success:
                runtime.enableVsockAgentListener()
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
