# Acme-Notmuch

This is a WIP mail reader for Acme, using Notmuch as the mail storage and query engine.

There are a few things missing that are required to make this useful:

* [ ] Removing the `unread` tag from read messages
* [ ] Mail authoring
	* [ ] Reply to some mail
	* [ ] Write an initial mail
* [ ] Listing and saving attachments
* [ ] Spam handling with bogofilter
	* [ ] Mark messages as Ham/Spam
* [ ] Switch between `text/plain` or `text/html` view for `multipart/alternative` messages
	* Currently, if a `text/html` part exists, it is rendered as text and shown.
	* If there is none, whatever the first part is will be shown

The following things _do_ work:

* Running queries and showing the results
* Showing messages, including rough HTML -> Text conversion for messages with MIME content type "text/html"
* Jumping to the next unread message in the thread of the currently open message