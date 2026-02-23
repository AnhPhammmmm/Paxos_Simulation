The goal: this project simulate Paxos. It recieve input from user, that simulate how computers connect with each other in Paxos.

The role: there are 3 struct was created:
- State: stores all nodes and messages that are sent between nodes.
- Node: simulates severs or computers in the real life.
- Key: used to access messages in struct State.

To run it, you need to type inputs with the same format as files in input folder.

Because this is Paxos simulation, and it runs on only one computer, so it lacks of some problems about network.
