#!/bin/bash
# Record with:
#   cd assets
#   asciinema rec demo.cast -c ./demo-record.sh --cols 90 --rows 35 --overwrite
#
# Convert:
#   agg demo.cast demo.gif --font-size 14 --idle-time-limit 2.5
#   ffmpeg -y -i demo.gif -movflags faststart -pix_fmt yuv420p -vf "scale=trunc(iw/2)*2:trunc(ih/2)*2" demo.mp4

DIR="$(cd "$(dirname "$0")" && pwd)"

clear
sleep 0.3

# Opening card
echo ""
echo ""
echo ""
echo ""
echo -e "       \033[1;36mweb-researcher-mcp\033[0m"
echo ""
echo -e "       Your AI research assistant that cites real"
echo -e "       sources and stays honest."
echo ""
echo -e "       \033[2mLinks that work. Citations you can trust.\033[0m"
echo ""
echo ""
sleep 3

clear
sleep 0.3

# Scene 1: Medical lens — trust & source control
"$DIR/demo-sim.sh" lenses
sleep 2.5

clear
sleep 0.3

# Scene 2: Academic search — real DOIs, no fabrication
"$DIR/demo-sim.sh" academic
sleep 2.5

clear
sleep 0.3

# Scene 3: Deep research — full articles, cited
"$DIR/demo-sim.sh" research
sleep 2.5

clear
sleep 0.3

# Scene 4: News — current events from real journalists
"$DIR/demo-sim.sh" news
sleep 2.5

clear
sleep 0.3

# Scene 5: Patent search — professional use case
"$DIR/demo-sim.sh" patents
sleep 2.5

clear
sleep 0.3

# Scene 6: Multi-step investigation
"$DIR/demo-sim.sh" sequential
sleep 2.5

clear
sleep 0.3

# Closing card
echo ""
echo ""
echo ""
echo ""
echo -e "       \033[1;36mweb-researcher-mcp\033[0m"
echo ""
echo -e "       Your AI research assistant that cites real"
echo -e "       sources and stays honest."
echo ""
echo -e "       \033[0;36mLinks that work | Citations you can trust | Private\033[0m"
echo ""
echo -e "       Works with Claude, Cursor, and any MCP-compatible AI"
echo ""
echo -e "       \033[4mgithub.com/zoharbabin/web-researcher-mcp\033[0m"
echo ""
sleep 4
