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

interface Agent {
  debug @0 () -> (debug :Debug);
  network @1 () -> (network :Network);
}
