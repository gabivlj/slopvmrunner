@0x9a11c8b7284a61de;

using Go = import "/go.capnp";

$Go.package("vmapi");
$Go.import("github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp");

interface ByteStream {
  write @0 (chunk :Data) -> stream;
  done @1 ();
}

interface Debug {
  ping @0 () -> (message :Text);
  openByteStream @1 () -> (stream :ByteStream);
  startBenchmarkVsock @2 (port :UInt32, totalBytes :UInt64, chunkBytes :UInt32) -> (bytesPerSec :Float64, durationNanos :UInt64);
  # openByteStream just serves the purpose of sending data.
}

interface Network {
  configureInterface @0 (ifName :Text, cidr :Text, gateway :Text);
  setupVsockProxy @1 (port :UInt32);
}

interface Task {
  stdin @0 () -> (stream :ByteStream);
  exitCode @1 () -> (code :Int32);
}

interface Container {
  start @0 (stdout :ByteStream, stderr :ByteStream) -> (task :Task);
}

interface ContainerService {
  create @0 (oci :Data, image :Text, id :Text) -> (container :Container);
}

interface Agent {
  debug @0 () -> (debug :Debug);
  network @1 () -> (network :Network);
  containerService @2 () -> (service :ContainerService);
}
