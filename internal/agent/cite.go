package agent

// citationInstructions ask the model to cite the files its final answer talks
// about with a tag, as AI search engines cite their sources: the interface
// shows each tag as a small chip, the file's name and lines, which opens code
// at those lines and any other file with the system's app for it.
const citationInstructions = "\nIn your final answer only (not in the messages you write between tool calls), cite the workspace files you refer to (code, images, documents, programs…) as a search engine cites its sources: inside the sentence it supports, right after the claim, write <cite file=\"path/from/the/workspace\"/>. For a code or text file, add lines=\"12-30\" (or lines=\"12\") when you point at part of it; never give lines for other files. Never gather citations in a list at the end, and do not also write the path and lines in the text. The user sees a small chip with the file's name and those lines, which opens the file there (a file that is not text opens with the user's app for it)."
