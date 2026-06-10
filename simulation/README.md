## Simulations
Some tests require the Johnny video file. Download it with `get-video.sh`.

Run the test in the terminal like that:
```
go test -tags simulation -v -run TestQUICh264Nada ./simulation
```
If you want to save the artifacts, run it like that:

```
go test -tags simulation -artifacts -v -run TestQUICh264Nada ./simulation
```
The results are saved in a folder named `_artifacts`.