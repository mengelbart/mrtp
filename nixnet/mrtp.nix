{ pkgs }:

pkgs.buildGoModule rec {
	pname = "mrtp";
	version = "unstable";

	src = builtins.path {
		name = "mrtp-src";
		path = ../.;
	};

	vendorHash = "sha256-+MfytoIqFB2oZk00PXXxsgAJNbqSVryAKBMlQdATkRk=";

	nativeBuildInputs = with pkgs; [
		pkg-config
	];

	buildInputs = with pkgs; [
		ffmpeg
		glib
		gst_all_1.gstreamer
		gst_all_1.gst-plugins-base
        gst_all_1.gst-plugins-good
        gst_all_1.gst-plugins-bad
        gst_all_1.gst-plugins-ugly
		libvpx
		x264
	];

	subPackages = [
		"cmd"
	];

	doCheck = false;

	postInstall = ''
		mv $out/bin/cmd $out/bin/mrtp
	'';

	meta = with pkgs.lib; {
		description = "Media streaming toolkit";
		homepage = "https://github.com/mengelbart/mrtp";
		license = licenses.mit;
		mainProgram = "mrtp";
		platforms = platforms.linux;
	};
}
