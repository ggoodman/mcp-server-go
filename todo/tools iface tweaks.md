# Tools and Progress

I would like to revisit how developers author tools in our library. We can borrow inspiration from go's `http.Handler` / `http.HandlerFunc` interfaces. In these, there is an immutable request and then a sort of response builder (aka `http.ResponseWriter`). This sort of design should give us an object we can use to send progress notifications and to construct complex responses with different bits of content of different types.

Further, we can refine the `Tool` interface / type to also take a json-schema-annotated struct for the response. If we can make it optional, even better; I'm not familiar enough with go's type system to know if we can pull it off. When supplied, the response schema should inform the response schema in tools listings.
