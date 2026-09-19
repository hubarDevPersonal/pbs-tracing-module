# Technical Assessment: Prebid Server Tracing Module

## Overview
Your task is to implement a custom Prebid Server module (in Go) that traces and collects specific auction data throughout the request lifecycle. 

## Provided Files
* `assessment/01-bid-request-example.json`: An example of the BidRequest payload that will be sent to the Prebid Server.
* `assessment/02-send-bid-request.sh`: A shell script containing the `curl` command to send the sample BidRequest.
* `pbs.yaml`: A ready-to-run Prebid Server configuration file.

## Task Description
Create a Prebid Server module that traces (collects) the following auction data:
1. The **incoming BidRequest** and its timestamp.
2. The **outgoing BidRequest** to a specific bidder, its timestamp, and the bidder's name.
3. The **incoming BidResponse** from a specific bidder, its timestamp, and the bidder's name.
4. The **resulting final Auction Response** that is sent back to the client, along with its timestamp.

## Conditions and Requirements
* **AI Assistance:** The use of AI-coding helpers (e.g., GitHub Copilot, ChatGPT, Claude) is **strictly advised**.
* **Output:** The collected trace object must be formatted as JSON and printed directly to the standard output (console).
* **Tracing Parameters:** Tracing rules **should be hardcoded** as set of rules(objects), each of them containing the following parameters: 
  * `PartnerID`
  * `Duration`
  * `TracePacketsAmount`
* **Trigger Condition:** A trace is initiated if an incoming BidRequest's `Account.ID` matches any rule's `PartnerID`.
* **Stop Conditions:** Tracing for a specific partner must stop when either of the following conditions is met (whichever occurs first):
  * **Time Limit:** The time elapsed since the *first* traced BidRequest for that partner exceeds the `Duration`.
  * **Amount Limit:** The total number of collected traces for the partner reaches `TracePacketsAmount`.
* **Account ID Mapping:** The `Account.ID` provided by the Prebid server maps directly to the `PartnerID` used in your tracing conditions.
* **Test Flag:** The provided `assessment/01-bid-request-example.json` includes the field `"test": 1`. This safely forces the Prebid Server to always return at least one valid BidResponse from the `appnexus` bidder, which is highly useful for validating your tracing logic.
* **Endpoint Scope:** The module should strictly affect the `/openrtb2/auction` endpoint. Any other endpoint is out of scope of the assessment.

## Reference Materials
Please refer to the official Prebid Server documentation for guidance on creating modules and understanding the request lifecycle:
* [Understand the Endpoints and Stages](https://docs.prebid.org/prebid-server/developers/add-a-module.html#2-understand-the-endpoints-and-stages)
* [Building a Go Module for Prebid Server](https://docs.prebid.org/prebid-server/developers/add-a-module-go.html)
