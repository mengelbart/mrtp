## Simulations
Some tests require the Johnny video file. Download it with `get-video.sh`.

Run the test in the terminal like that:
```
go test -tags simulation -v -run TestQUICh264Nada ./simulation
```
Each test writes its results to `./result` (override each other).