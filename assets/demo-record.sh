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
echo -e "       Web search, scraping, and multi-source"
echo -e "       research tools for AI assistants"
echo ""
echo -e "       \033[2mSingle binary | Multiple providers | MIT\033[0m"
echo ""
echo ""
sleep 3

clear
sleep 0.3

# Scene 1: News — anyone can relate to "what's happening today?"
"$DIR/demo-sim.sh" news
sleep 2.5

clear
sleep 0.3

# Scene 2: Research with synthesis — show the full value loop
"$DIR/demo-sim.sh" research
sleep 2.5

clear
sleep 0.3

# Scene 3: Medical lens — powerful even for non-devs (health research)
"$DIR/demo-sim.sh" lenses
sleep 2.5

clear
sleep 0.3

# Scene 4: Patent search — advanced professional use case
"$DIR/demo-sim.sh" patents
sleep 2.5

clear
sleep 0.3

# Scene 5: Intelligent scraping with tier escalation
"$DIR/demo-sim.sh" scrape
sleep 2.5

clear
sleep 0.3

# Scene 6: Sequential multi-step research (most powerful capability)
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
echo -e "       Web research superpowers for any AI assistant"
echo ""
echo -e "       \033[0;36mMultiple search providers | Domain lenses | Patent search\033[0m"
echo -e "       \033[0;36mSmart scraping pipeline | Sequential research sessions\033[0m"
echo ""
echo -e "       Works with Claude Code, Cursor, VS Code, and any MCP client"
echo ""
echo -e "       \033[4mgithub.com/zoharbabin/web-researcher-mcp\033[0m"
echo ""
sleep 4
