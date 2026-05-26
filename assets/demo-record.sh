#!/bin/bash
# Record with:
#   cd assets
#   asciinema rec demo.cast -c ./demo-record.sh --cols 90 --rows 35 --overwrite
#
# Convert:
#   agg demo.cast demo.gif --font-size 14 --idle-time-limit 3
#   ffmpeg -y -i demo.gif -movflags faststart -pix_fmt yuv420p -vf "scale=trunc(iw/2)*2:trunc(ih/2)*2" demo.mp4

DIR="$(cd "$(dirname "$0")" && pwd)"

clear
sleep 0.5

# Scene 1: News — anyone can relate to "what's happening today?"
"$DIR/demo-sim.sh" news
sleep 3

clear
sleep 0.5

# Scene 2: Research with synthesis — show the full value loop
"$DIR/demo-sim.sh" research
sleep 3

clear
sleep 0.5

# Scene 3: Medical lens — powerful even for non-devs (health research)
"$DIR/demo-sim.sh" lenses
sleep 3

clear
sleep 0.5

# Scene 4: Patent search — advanced professional use case
"$DIR/demo-sim.sh" patents
sleep 3

clear
sleep 0.5

# Scene 5: Intelligent scraping — shows content negotiation tiers (dev-facing)
"$DIR/demo-sim.sh" scrape
sleep 3

clear
sleep 0.5

# Scene 6: Scraping with tier escalation — shows fallback pipeline (dev-facing)
"$DIR/demo-sim.sh" scrape_js
sleep 3

clear
sleep 0.5

# Closing card
echo ""
echo ""
echo ""
echo ""
echo ""
echo -e "       \033[1;36mweb-researcher-mcp\033[0m"
echo ""
echo -e "       Web research superpowers for any AI assistant"
echo ""
echo -e "       \033[0;36m8 tools · 5 providers · Single binary · MIT License\033[0m"
echo ""
echo -e "       Works with Claude Code, Cursor, and any MCP client"
echo ""
echo -e "       \033[4mgithub.com/zoharbabin/web-researcher-mcp\033[0m"
echo ""
sleep 5
