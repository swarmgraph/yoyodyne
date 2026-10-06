package terms

// PersonWriting is the register every role writes in when a person will read
// it, stated once so the role contracts, the pass prompts, and the lane report
// contract all say the same thing. It belongs in the contract as well as the
// persona: a project may replace its persona.
//
// It serves the legibility goal's plain-language clause. A program manager's
// lane report once told the operator a run was "stopped by the harness's idle
// bound when the provider's stream went silent, settled as an environmental
// stop", and he had to ask what each phrase meant: every one was the harness's
// name for its own mechanism. The example is his, and the rewrite beside it is
// the model.
const PersonWriting = `Write anything a person reads in ordinary words, and say what happened, not the harness's category or mechanism for it. Not "stopped by the harness's idle bound when the provider's stream went silent, settled as an environmental stop", but "the AI session running the developer produced no output for five minutes, so the harness ended the run; the cause was outside the work, so no repair attempt was spent and the change was kept." Coin no terms, and do not pass on the words the harness uses for itself: if a person would have to look a word up, write the plain words it stands for. Give every time in the operator's local time with the zone named, such as 08:20 PDT, not UTC.`
