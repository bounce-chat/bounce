# Goals

### 1. Protect Metadata

Bounce should make it difficult for an adversary with broad observation capabilities over the internet to know which users are communicating with each other.  This is accomplished by having instances of Bounce only connect to each other via a mixnet (currently, Tor hidden services).  Preventing this same adversary from knowing if an individual is a user of Bounce remains a non-goal for now.  Replacing Tor with a purpose built mixnet is a future goal.

### 2. User's data should only exist on user's hardware

Data, either plaintext or encrypted, should only pass through, and rest on, devices that are owned by someone who is authorized to read the content.  If a device is compromised, it should not result in the theft of any data or metadata unrelated to the compromised user, or someone they were communicating with.

There is flexibility here for offering a hosting service for encrypted devices.  I may choose to do this at a future date, in which case I as the hosting provider would have access to limited metadata via hosted encrypted devices, for those who see the availability gains as worth the reduction in control over metadata.

### 3. Keep the network flat

The network architecture should not have any special server nodes that are required for two devices to communicate.  There should be no central points of failure in the network; if two users are able to access the internet, they should always be able to communicate.

There is flexibility here for Tor, which is not an entirely flat network, but has an excellent track record for uptime and is an acceptable host network for now.  There is no flexibility here within Bounce's architecture.

# Non-Goals

### 1. Perfect offline delivery

If user A, who only owns one device, attempts to send a message to user B, who is entirely offline, and user A takes their device offline before user B comes online, than user A's message will not be received by user B when user B comes online.  Any solution to this problem would necessarily violate the second goal of the project, and so solving this case is an explicit non-goal.  In order to minimize the impact of this limitation, encrypted devices were created to give people the ability to host an instance in the cloud with minimal risk.  Paid hosting of encrypted devices might be provided as an option to reduce the difficulty of setting this up.

### 2. Global user namespace

Bounce is not intended to be a place to meet strangers, and there will never be a way to search across all users of Bounce.  Bounce is intended to connect people who already know each other offline, and for people to meet friends of friends via shared groups.  A benefit of this design is that it eliminates spam.
