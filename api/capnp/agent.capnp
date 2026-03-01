@0x9a11c8b7284a61de;

using Go = import "/go.capnp";

$Go.package("vmapi");
$Go.import("github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp");

interface ByteStream {
  write @0 (chunk :Data);
  done @1 ();
}

interface Agent {
  ping @0 () -> (message :Text);
  openByteStream @1 () -> (stream :ByteStream);
}
