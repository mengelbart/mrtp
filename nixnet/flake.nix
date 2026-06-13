{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixnet.url = "github:birneee/nixnet";
  };
  outputs =
    inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      perSystem = 
      { pkgs, inputs', ...}:
      let
        nixnet = inputs'.nixnet.legacyPackages;
        mrtp = import ./mrtp.nix { inherit pkgs; };
        config = {
          arp = false;
          arpPrefill = true;
          namespacePackages = with pkgs; [ 
            bash
            iperf3
            coreutils
          ];
          namespaces = {
            client = {
              networking.interfaces.veth0.ipv4 = {
                addresses = [
                  {
                    address = "10.0.1.2";
                    prefixLength = 24;
                  }
                ];
                routes = [
                  {
                    address = "10.0.2.0";
                    prefixLength = 24;
                    via = "10.0.1.1";
                  }
                ];
              };
              scripts = [
                    #iperf3 -c 10.0.2.2 > ./stdout 2>&1
                {
                  #exec = ''
                  #  sleep 0.1
                  #  "${mrtp}/bin/mrtp send -source-location videotestsrc"
                  #'';
                  exec = "echo hello";
                  await = true;
                }
              ];
              workDir = "./client";
            };
            client-router = {
              networking.interfaces = {
                veth0.ipv4.addresses = [
                  {
                    address = "10.0.1.1";
                    prefixLength = 24;
                  }
                ];
                veth1.ipv4.addresses = [
                  {
                    address = "10.0.0.1";
                    prefixLength = 24;
                  }
                ];
                veth1.ipv4.routes = [
                  {
                    address = "10.0.2.0";
                    prefixLength = 24;
                    via = "10.0.0.2";
                  }
                ];
              };
              sysctl."net.ipv4.ip_forward" = true;
            };
            server = {
              networking.interfaces.veth0.ipv4 = {
                addresses = [
                  {
                    address = "10.0.2.2";
                    prefixLength = 24;
                  }
                ];
                routes = [
                  {
                    address = "10.0.1.0";
                    prefixLength = 24;
                    via = "10.0.2.1";
                  }
                ];
              };
              packages = with pkgs; [
                ffmpeg
                glib
                gst_all_1.gstreamer
                gst_all_1.gst-plugins-base
                gst_all_1.gst-plugins-good
                gst_all_1.gst-plugins-bad
                gst_all_1.gst-plugins-ugly
                gst_all_1.gst-libav
                libvpx
                x264
              ];
              scripts = [
                {
                  # exec = "iperf3 -s > ./stdout 2>&1";
                  # exec = "GST_DEBUG=5 ${mrtp}/bin/mrtp receive";
                  exec = ''
                    bash
                  '';
                  foreground = true;
                  await = true;
                }
              ];
              workDir = "./server";
            };
            server-router = {
              networking.interfaces = {
                veth0.ipv4.addresses = [
                  {
                    address = "10.0.2.1";
                    prefixLength = 24;
                  }
                ];
                veth1.ipv4.addresses = [
                  {
                    address = "10.0.0.2";
                    prefixLength = 24;
                  }
                ];
                veth1.ipv4.routes = [
                  {
                    address = "10.0.1.0";
                    prefixLength = 24;
                    via = "10.0.0.1";
                  }
                ];
              };
              sysctl."net.ipv4.ip_forward" = true;
            };
          };
          veths = [
            {
              a = {
                ns = "client";
                iface = "veth0";
              };
              b = {
                ns = "client-router";
                iface = "veth0";
              };
            }
            {
              netem.delayMs = 50;
              netem.rateMbit = 1;
              a = {
                ns = "client-router";
                iface = "veth1";
              };
              b = {
                ns = "server-router";
                iface = "veth1";
              };
            }
            {
              a = {
                ns = "server-router";
                iface = "veth0";
              };
              b = {
                ns = "server";
                iface = "veth0";
              };
            }
          ];
        };
      in
      {
        packages.mrtp = mrtp;
        packages.default = nixnet.mkTestbed config;
        packages.mermaid = nixnet.mkMermaid config;
        packages.mermaid-svg =  nixnet.mkMermaidSvg config;
      };
    };
}