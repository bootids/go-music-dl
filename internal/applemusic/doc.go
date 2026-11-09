// Package applemusic downloads full Apple Music tracks through an external
// wrapper-lite service and decrypts them in-process.
//
// Attribution:
//   - The HTTP protocol spoken by Client (/status, /m3u8, /key, /lyrics) is
//     defined by wrapper-lite, WorldObservationLog/wrapper (lite branch), MIT.
//   - The FairPlay sample decryptor in the temari subpackage is a Go port of
//     Temari (MIT); see temari/LICENSE.
//   - zhaarey/apple-music-downloader was used as a behavioural reference only;
//     no code from it is copied into this package.
package applemusic

// PrefetchKeyURI is the key URI of the shared prefetch context. wrapper-lite
// accepts it only together with adamId "0".
const PrefetchKeyURI = "skd://itunes.apple.com/P000000000/s1/e1"
