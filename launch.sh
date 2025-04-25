#!/bin/bash

SESSION="cis"
PROGRAM="./composite_image_server"
COMPORT="/dev/ttyUSB0"

stty -F $COMPORT 115200 cs8 -cstopb -parenb

echo Composite Image Server Running > /dev/ttyUSB0

# Check if the tmux session already exists
tmux has-session -t $SESSION 2>/dev/null

if [ $? != 0 ]; then
  echo "Creating new tmux session: $SESSION"
  tmux new-session -d -s $SESSION
else
  echo "Session $SESSION already exists."
fi

# Kill any existing process running in the first pane of the session
echo "Stopping existing process in tmux session..."
tmux send-keys -t $SESSION "C-c" # Sends Ctrl+C to terminate running process
sleep 1                          # Give it a second to die

# Clear the pane (optional, just to clean up)
tmux send-keys -t $SESSION "clear" C-m

# Run the program
echo "Starting new program in session $SESSION..."
tmux send-keys -t $SESSION "$PROGRAM" C-m

# Run the monitor
echo "Starting a new window in tmux sessions $SESSION..."
tmux new-window -t "$SESSION" -n "ls-window" "./check_web.sh http://localhost:8080/ /dev/ttyUSB0 60"

# Attach to the session
tmux attach -t $SESSION
