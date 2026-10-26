package agent

// citationInstructions ask the model to cite the code its final answer talks
// about with a tag, as AI search engines cite their sources: the interface
// shows each tag as a small chip, the file's name and lines, which opens the
// file at those lines.
const citationInstructions = "\nIn your final answer only (not in the messages you write between tool calls), cite the workspace code you refer to as a search engine cites its sources: inside the sentence it supports, right after the claim, write <cite file=\"path/from/the/workspace\" lines=\"12-30\"/> (lines=\"12\" for one line; no lines for a whole file). Never gather citations in a list at the end, and do not also write the path and lines in the text. The user sees a small chip with the file's name and those lines, which opens the file there."
